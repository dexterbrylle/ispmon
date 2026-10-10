import logging
import os
import time
from pathlib import Path
import schedule

from monitor.config import (
    CHECK_INTERVAL_MINUTES,
    SUMMARY_INTERVAL_HOURS,
    DAILY_REPORT_TIME,
    DB_PATH
)
from monitor.speedtest import perform_speed_test, get_public_ip
import monitor.db as db
from monitor.sla import evaluate_check, check_sla_breach, handle_breach_state
from monitor.outage import handle_outage_state
from monitor.reports import send_email_report

logger = logging.getLogger(__name__)


def touch_heartbeat():
    """Touch a heartbeat file in the data directory for health checks."""
    try:
        data_dir = os.path.dirname(DB_PATH) or "data"
        Path(data_dir).mkdir(parents=True, exist_ok=True)
        heartbeat_file = os.path.join(data_dir, ".heartbeat")
        with open(heartbeat_file, "w") as f:
            f.write(str(time.time()))
    except Exception as e:
        logger.debug(f"Heartbeat write error: {e}")


def perform_network_check():
    """Run a network speed test, persist metrics, and evaluate SLA/outage status."""
    touch_heartbeat()
    logger.info("Starting network performance check...")

    metrics = perform_speed_test()
    ip_address = get_public_ip()

    download_speed = metrics.get('download_speed')
    upload_speed = metrics.get('upload_speed')
    latency = metrics.get('latency')
    jitter = metrics.get('jitter')
    packet_loss = metrics.get('packet_loss')
    server_name = metrics.get('server_name')
    result_url = metrics.get('result_url')
    error_msg = metrics.get('error')

    is_successful = (download_speed is not None and upload_speed is not None)

    if is_successful:
        below = evaluate_check(download_speed, upload_speed)
        db.save_check_results(
            download_speed=download_speed,
            upload_speed=upload_speed,
            latency=latency,
            ip_address=ip_address,
            below_threshold=below,
            status='ok',
            error=None,
            jitter=jitter,
            packet_loss=packet_loss,
            server_name=server_name,
            result_url=result_url
        )

        logger.info(
            f"Check completed — Download: {download_speed:.2f} Mbps, "
            f"Upload: {upload_speed:.2f} Mbps, Latency: {latency:.2f} ms "
            f"({'BELOW SLA' if below else 'OK'})"
        )

        # Connection is healthy: resolve any active outage
        handle_outage_state(is_successful=True, reason="", db=db)

        # Evaluate contractual SLA
        sla_result = check_sla_breach(db)
        handle_breach_state(sla_result, db)
    else:
        logger.warning(f"Network check failed — Speed test error: {error_msg}")
        db.save_check_results(
            download_speed=None,
            upload_speed=None,
            latency=None,
            ip_address=ip_address,
            below_threshold=False,
            status='failed',
            error=error_msg or "Speed test measurement failed"
        )

        # Connection failed: manage outage tracking (D1)
        handle_outage_state(
            is_successful=False,
            reason=error_msg or "Speed test measurement failed",
            db=db
        )


def setup_schedule():
    """Configure scheduled tasks."""
    schedule.every(CHECK_INTERVAL_MINUTES).minutes.do(perform_network_check)
    schedule.every(SUMMARY_INTERVAL_HOURS).hours.do(lambda: send_email_report(SUMMARY_INTERVAL_HOURS))
    schedule.every().day.at(DAILY_REPORT_TIME).do(lambda: send_email_report(24))
    logger.info(
        f"Schedule configured: Check every {CHECK_INTERVAL_MINUTES}m, "
        f"Summary every {SUMMARY_INTERVAL_HOURS}h, Daily report at {DAILY_REPORT_TIME}"
    )


def run_loop():
    """Main daemon loop."""
    logger.info("Performing initial network check on startup...")
    perform_network_check()

    logger.info("Entering monitoring schedule loop...")
    while True:
        try:
            schedule.run_pending()
            time.sleep(1)
        except Exception as e:
            logger.error(f"Error in scheduler loop: {e}")
            time.sleep(30)
