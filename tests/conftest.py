import os
import tempfile
import pytest
from monitor.db import init_db

# Configure test environment variables
os.environ['DB_PATH'] = ':memory:'
os.environ['ISP_PLAN_NAME'] = 'Test Plan 500'
os.environ['ISP_PLAN_THRESHOLD'] = '150'
os.environ['ISP_PLAN_UPLOAD_THRESHOLD'] = '150'
os.environ['SLA_WINDOW_HOURS'] = '24'
os.environ['SLA_VIOLATION_PCT'] = '20.0'
os.environ['SLA_MIN_COVERAGE'] = '0.75'
os.environ['CHECK_INTERVAL_MINUTES'] = '30'
os.environ['DISCORD_WEBHOOK_URL'] = 'https://discord.com/api/webhooks/test'
os.environ['SMTP_SERVER'] = 'smtp.test.com'
os.environ['SMTP_USER'] = 'test@test.com'
os.environ['SMTP_PASSWORD'] = 'secret'
os.environ['EMAIL_RECIPIENT'] = 'alerts@test.com'


@pytest.fixture
def test_db_path(tmp_path):
    """Provide a fresh isolated SQLite database file for each test."""
    db_file = str(tmp_path / "test_monitor.db")
    init_db(db_file)
    return db_file
