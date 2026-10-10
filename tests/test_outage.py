from unittest.mock import patch, Mock
from monitor.outage import handle_outage_state


class MockOutageDB:
    def __init__(self, active=None):
        self.active = active
        self.started = None
        self.ended_id = None

    def get_active_outage_episode(self):
        return self.active

    def start_outage_episode(self, reason):
        self.started = reason
        self.active = {"id": 1, "reason": reason}
        return 1

    def end_outage_episode(self, episode_id):
        self.ended_id = episode_id
        self.active = None
        return 120  # 2 minutes duration


@patch('monitor.alerts.send_discord_outage_alert')
def test_outage_starts_on_failure(mock_alert):
    """When a network check fails, start an outage episode and fire alert."""
    db = MockOutageDB(active=None)
    handle_outage_state(is_successful=False, reason="DNS resolution failed", db=db)
    assert db.started == "DNS resolution failed"
    mock_alert.assert_called_once_with("DNS resolution failed")


@patch('monitor.alerts.send_discord_outage_alert')
def test_outage_suppressed_when_already_active(mock_alert):
    """Continuing outage does not spawn duplicate episodes or alerts."""
    db = MockOutageDB(active={"id": 1, "reason": "Initial drop"})
    handle_outage_state(is_successful=False, reason="Still down", db=db)
    assert db.started is None
    mock_alert.assert_not_called()


@patch('monitor.alerts.send_discord_outage_resolved')
def test_outage_resolves_on_success(mock_resolved):
    """When a check succeeds after an outage, close episode and send restoration notice."""
    db = MockOutageDB(active={"id": 1})
    handle_outage_state(is_successful=True, reason="", db=db)
    assert db.ended_id == 1
    mock_resolved.assert_called_once_with(120)
