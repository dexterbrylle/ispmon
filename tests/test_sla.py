from unittest.mock import patch
from monitor.sla import evaluate_check, check_sla_breach, handle_breach_state


def test_evaluate_check_normal():
    """Speeds above threshold should not be flagged as violating."""
    assert evaluate_check(500.0, 500.0) is False
    assert evaluate_check(151.0, 151.0) is False


def test_evaluate_check_below_threshold():
    """Speeds below contractual minimum (150 Mbps) should evaluate True."""
    assert evaluate_check(149.0, 500.0) is True
    assert evaluate_check(500.0, 149.0) is True
    assert evaluate_check(100.0, 100.0) is True


def test_evaluate_check_none_values():
    """None values (failed tests) should return False — handled by outage tracker (D1)."""
    assert evaluate_check(None, 500.0) is False
    assert evaluate_check(500.0, None) is False
    assert evaluate_check(None, None) is False


class MockDB:
    """Mock database adapter for SLA logic unit tests."""
    def __init__(self, checks=None, episode=None):
        self.checks = checks or []
        self.episode = episode
        self.started_id = None
        self.updated = None
        self.ended = False

    def get_recent_checks(self, hours=24):
        return self.checks

    def get_active_breach_episode(self):
        return self.episode

    def start_breach_episode(self, violating_count, total_count, violation_pct, worst_down, worst_up):
        self.started_id = 99
        self.episode = {"id": 99}
        return 99

    def update_breach_episode(self, episode_id, worst_down, worst_up, violating_count, total_count, violation_pct):
        self.updated = (episode_id, worst_down, worst_up, violating_count, total_count, violation_pct)

    def end_breach_episode(self, episode_id):
        self.ended = True
        self.episode = None


def test_sla_insufficient_data():
    """Fewer valid checks than minimum required coverage should defer SLA evaluation."""
    db = MockDB(checks=[{"status": "ok", "download_speed": 100, "upload_speed": 100}] * 10)
    result = check_sla_breach(db)
    assert result["status"] == "insufficient_data"
    assert result["check_count"] == 10


def test_sla_no_breach():
    """All valid checks above threshold should evaluate ok."""
    checks = [{"status": "ok", "download_speed": 500.0, "upload_speed": 500.0}] * 48
    db = MockDB(checks=checks)
    result = check_sla_breach(db)
    assert result["status"] == "ok"
    assert result["violating_count"] == 0
    assert result["violation_pct"] == 0.0


def test_sla_breach_detected():
    """More than 20% checks below 150 Mbps should declare a breach."""
    checks = [{"status": "ok", "download_speed": 500.0, "upload_speed": 500.0}] * 38
    checks += [{"status": "ok", "download_speed": 100.0, "upload_speed": 90.0}] * 10
    db = MockDB(checks=checks)
    result = check_sla_breach(db)
    assert result["status"] == "breach"
    assert result["violating_count"] == 10
    assert result["total_count"] == 48
    assert result["worst_download"] == 100.0
    assert result["worst_upload"] == 90.0
    assert result["violation_pct"] == round((10 / 48) * 100, 1)


def test_sla_d1_outages_excluded_from_speed_ratio():
    """Failed tests (outages) should not dilute or distort the speed violation ratio."""
    # 30 good checks, 10 slow checks = 40 valid checks. 10/40 = 25% (>20% -> breach!)
    # Plus 8 failed checks (outages)
    checks = [{"status": "ok", "download_speed": 500.0, "upload_speed": 500.0}] * 30
    checks += [{"status": "ok", "download_speed": 100.0, "upload_speed": 100.0}] * 10
    checks += [{"status": "failed", "download_speed": None, "upload_speed": None}] * 8

    db = MockDB(checks=checks)
    result = check_sla_breach(db)

    # 40 valid checks out of 48 total checks
    assert result["total_count"] == 40
    assert result["all_window_count"] == 48
    assert result["violating_count"] == 10
    assert result["violation_pct"] == 25.0
    assert result["status"] == "breach"


@patch('monitor.alerts.send_discord_alert')
@patch('monitor.alerts.send_breach_email')
def test_handle_breach_state_new_breach(mock_email, mock_discord):
    """When a new breach is detected, start episode and fire alerts."""
    db = MockDB(episode=None)
    result = {
        "status": "breach",
        "violating_count": 12,
        "total_count": 48,
        "violation_pct": 25.0,
        "worst_download": 50.0,
        "worst_upload": 40.0,
        "breached_checks": []
    }
    handle_breach_state(result, db)
    assert db.started_id == 99
    mock_discord.assert_called_once()
    mock_email.assert_called_once()


@patch('monitor.alerts.send_discord_alert')
@patch('monitor.alerts.send_breach_email')
def test_handle_breach_state_active_episode(mock_email, mock_discord):
    """During continuing breach, update episode and suppress duplicate alerts."""
    db = MockDB(episode={"id": 10})
    result = {
        "status": "breach",
        "violating_count": 15,
        "total_count": 48,
        "violation_pct": 31.2,
        "worst_download": 45.0,
        "worst_upload": 35.0,
        "breached_checks": []
    }
    handle_breach_state(result, db)
    assert db.updated is not None
    mock_discord.assert_not_called()
    mock_email.assert_not_called()


@patch('monitor.alerts.send_discord_resolved')
def test_handle_breach_state_resolved(mock_resolved):
    """When speeds return to normal, end active episode and send resolved alert."""
    db = MockDB(episode={"id": 10, "started_at": "2026-10-10T00:00:00Z", "violation_pct": 25.0})
    result = {
        "status": "ok",
        "violating_count": 2,
        "total_count": 48,
        "violation_pct": 4.1,
        "worst_download": 300.0,
        "worst_upload": 300.0,
        "breached_checks": []
    }
    handle_breach_state(result, db)
    assert db.ended is True
    mock_resolved.assert_called_once()
