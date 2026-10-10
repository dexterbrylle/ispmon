import os
import logging
from pathlib import Path
from dotenv import load_dotenv

load_dotenv()

# Logging configuration
log_handlers = [logging.StreamHandler()]
log_dir = os.getenv('LOG_DIR', '')
if log_dir:
    Path(log_dir).mkdir(parents=True, exist_ok=True)
    log_handlers.append(logging.FileHandler(os.path.join(log_dir, 'network_monitor.log')))

logging.basicConfig(
    level=logging.INFO,
    format='%(asctime)s - %(levelname)s - %(name)s - %(message)s',
    handlers=log_handlers
)

logger = logging.getLogger(__name__)

# Database
DB_PATH = os.getenv('DB_PATH', 'data/network_monitor.db')

# Speed alert thresholds (for periodic report "slow incident" counts)
SPEED_ALERT_DOWNLOAD = float(os.getenv('SPEED_ALERT_DOWNLOAD', '500'))
SPEED_ALERT_UPLOAD = float(os.getenv('SPEED_ALERT_UPLOAD', '300'))

# ISP Plan Configuration (contractual SLA)
ISP_PLAN_NAME = os.getenv('ISP_PLAN_NAME', 'GFiber Plan 1999')
ISP_PLAN_SPEED = int(os.getenv('ISP_PLAN_SPEED', '500'))
ISP_PLAN_UPLOAD_SPEED = int(os.getenv('ISP_PLAN_UPLOAD_SPEED', '500'))
ISP_PLAN_THRESHOLD = float(os.getenv('ISP_PLAN_THRESHOLD', '150'))
ISP_PLAN_UPLOAD_THRESHOLD = float(os.getenv('ISP_PLAN_UPLOAD_THRESHOLD', '150'))

# SLA rule configuration
SLA_WINDOW_HOURS = int(os.getenv('SLA_WINDOW_HOURS', '24'))
SLA_VIOLATION_PCT = float(os.getenv('SLA_VIOLATION_PCT', '20.0'))
SLA_MIN_COVERAGE = float(os.getenv('SLA_MIN_COVERAGE', '0.75'))

# Discord alerts
DISCORD_WEBHOOK_URL = os.getenv('DISCORD_WEBHOOK_URL', '').strip()

# Email configuration
SMTP_SERVER = os.getenv('SMTP_SERVER', '').strip()
SMTP_PORT = int(os.getenv('SMTP_PORT', '587'))
SMTP_USER = os.getenv('SMTP_USER', '').strip()
SMTP_PASSWORD = os.getenv('SMTP_PASSWORD', '').strip()
EMAIL_RECIPIENT = os.getenv('EMAIL_RECIPIENT', '').strip()

# Monitoring schedule
CHECK_INTERVAL_MINUTES = int(os.getenv('CHECK_INTERVAL_MINUTES', '30'))
SUMMARY_INTERVAL_HOURS = int(os.getenv('SUMMARY_INTERVAL_HOURS', '6'))
DAILY_REPORT_TIME = os.getenv('DAILY_REPORT_TIME', '23:59')


def is_email_configured() -> bool:
    """Check if SMTP email reporting is fully configured."""
    return bool(SMTP_SERVER and SMTP_USER and SMTP_PASSWORD and EMAIL_RECIPIENT)


def is_discord_configured() -> bool:
    """Check if Discord webhook alerts are configured."""
    return bool(DISCORD_WEBHOOK_URL)


if not is_discord_configured():
    logger.info("Discord webhook not configured — Discord alerts disabled")

if not is_email_configured():
    logger.info("SMTP email not fully configured — email reports disabled")
