import logging

logger = logging.getLogger(__name__)


def handle_outage_state(is_successful: bool, reason: str, db):
    """Manage lifecycle state for connectivity outage episodes."""
    active = db.get_active_outage_episode()

    if not is_successful and not active:
        episode_id = db.start_outage_episode(reason=reason)
        if episode_id:
            try:
                from monitor.alerts import send_discord_outage_alert
                send_discord_outage_alert(reason)
            except Exception as e:
                logger.error(f"Failed sending outage alert: {e}")

    elif is_successful and active:
        duration = db.end_outage_episode(active["id"])
        try:
            from monitor.alerts import send_discord_outage_resolved
            send_discord_outage_resolved(duration or 0)
        except Exception as e:
            logger.error(f"Failed sending outage resolved alert: {e}")
