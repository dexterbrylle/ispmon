import logging
from monitor.config import (
    ISP_PLAN_THRESHOLD,
    ISP_PLAN_UPLOAD_THRESHOLD,
    SLA_WINDOW_HOURS,
    SLA_VIOLATION_PCT,
    SLA_MIN_COVERAGE,
    CHECK_INTERVAL_MINUTES,
    ISP_PLAN_NAME
)

logger = logging.getLogger(__name__)


def evaluate_check(download, upload):
    """Returns True if this single check is below contractual minimum speed.
    Returns False if either value is None (failed check handled as outage, not speed violation).
    """
    if download is None or upload is None:
        return False

    return download < ISP_PLAN_THRESHOLD or upload < ISP_PLAN_UPLOAD_THRESHOLD


def check_sla_breach(db):
    """Evaluate SLA breach status across recent valid checks.
    Returns dict with status, metrics, and breached check records.
    """
    recent = db.get_recent_checks(hours=SLA_WINDOW_HOURS)
    expected_checks = max(1, int((SLA_WINDOW_HOURS * 60) / CHECK_INTERVAL_MINUTES))
    min_required = max(1, int(expected_checks * SLA_MIN_COVERAGE))

    # D1 decision: Filter valid speed checks (exclude failed test outages from speed ratio)
    valid_checks = [
        c for c in recent 
        if c.get('status') == 'ok' 
        and c.get('download_speed') is not None 
        and c.get('upload_speed') is not None
    ]

    total_valid = len(valid_checks)

    if total_valid < min_required:
        logger.info(
            f"SLA evaluation deferred — insufficient data ({total_valid}/{min_required} required valid checks, "
            f"expected {expected_checks})"
        )
        return {
            "status": "insufficient_data",
            "check_count": total_valid,
            "min_required": min_required,
            "expected_count": expected_checks
        }

    violating_checks = []
    worst_download = float('inf')
    worst_upload = float('inf')

    for check in valid_checks:
        dl = check.get('download_speed')
        ul = check.get('upload_speed')

        if evaluate_check(dl, ul):
            violating_checks.append(check)
            if dl is not None and dl < worst_download:
                worst_download = dl
            if ul is not None and ul < worst_upload:
                worst_upload = ul

    if worst_download == float('inf'):
        worst_download = None
    if worst_upload == float('inf'):
        worst_upload = None

    violating_count = len(violating_checks)
    violation_pct = (violating_count / total_valid) * 100.0
    is_breach = violation_pct > SLA_VIOLATION_PCT

    logger.info(
        f"SLA eval: {violating_count}/{total_valid} below threshold ({violation_pct:.1f}% vs rule >{SLA_VIOLATION_PCT}%) — "
        f"{'BREACH' if is_breach else 'OK'}"
    )

    return {
        "status": "breach" if is_breach else "ok",
        "violating_count": violating_count,
        "total_count": total_valid,
        "all_window_count": len(recent),
        "violation_pct": round(violation_pct, 1),
        "worst_download": worst_download,
        "worst_upload": worst_upload,
        "breached_checks": violating_checks,
        "threshold": ISP_PLAN_THRESHOLD,
        "upload_threshold": ISP_PLAN_UPLOAD_THRESHOLD,
        "plan_name": ISP_PLAN_NAME
    }


def handle_breach_state(result, db):
    """Manage lifecycle state for breach episodes (start, update, resolve)."""
    if result.get("status") == "insufficient_data":
        return

    active = db.get_active_breach_episode()

    if result["status"] == "breach" and not active:
        episode_id = db.start_breach_episode(
            violating_count=result["violating_count"],
            total_count=result["total_count"],
            violation_pct=result["violation_pct"],
            worst_down=result["worst_download"],
            worst_up=result["worst_upload"]
        )

        if episode_id:
            from monitor.alerts import send_discord_alert, send_breach_email
            send_discord_alert(result)
            send_breach_email(result)
            logger.info(f"SLA breach alert fired — episode {episode_id}")

    elif result["status"] == "breach" and active:
        db.update_breach_episode(
            episode_id=active["id"],
            worst_down=result["worst_download"],
            worst_up=result["worst_upload"],
            violating_count=result["violating_count"],
            total_count=result["total_count"],
            violation_pct=result["violation_pct"]
        )
        logger.debug(f"SLA breach episode {active['id']} updated")

    elif result["status"] == "ok" and active:
        db.end_breach_episode(active["id"])
        from monitor.alerts import send_discord_resolved
        send_discord_resolved(active)
        logger.info(f"SLA breach resolved — episode {active['id']} closed")
