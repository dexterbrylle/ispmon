import os
from unittest.mock import patch
from monitor.scheduler import perform_network_check, touch_heartbeat


def test_touch_heartbeat(tmp_path):
    """Verify heartbeat file is created and updated."""
    test_db = str(tmp_path / "data" / "test.db")
    with patch('monitor.scheduler.DB_PATH', test_db):
        touch_heartbeat()
        hb_path = str(tmp_path / "data" / ".heartbeat")
        assert os.path.exists(hb_path)
        with open(hb_path) as f:
            content = f.read().strip()
            assert float(content) > 0


@patch('monitor.scheduler.perform_speed_test')
@patch('monitor.scheduler.get_public_ip')
@patch('monitor.scheduler.db.save_check_results')
@patch('monitor.scheduler.check_sla_breach')
@patch('monitor.scheduler.handle_breach_state')
@patch('monitor.scheduler.handle_outage_state')
def test_perform_network_check_success(
    mock_outage,
    mock_breach_handle,
    mock_breach_check,
    mock_save,
    mock_ip,
    mock_speedtest
):
    """Successful check should save row with ok status and evaluate SLA."""
    mock_speedtest.return_value = {
        "download_speed": 480.0,
        "upload_speed": 450.0,
        "latency": 10.0,
        "jitter": 1.1,
        "packet_loss": 0.0,
        "server_name": "Test Server",
        "result_url": "https://speedtest.net/result/123",
        "error": None
    }
    mock_ip.return_value = "1.2.3.4"
    mock_breach_check.return_value = {"status": "ok"}

    perform_network_check()

    mock_save.assert_called_once()
    saved_kwargs = mock_save.call_args[1]
    assert saved_kwargs['download_speed'] == 480.0
    assert saved_kwargs['status'] == 'ok'
    assert saved_kwargs['below_threshold'] is False

    mock_outage.assert_called_once_with(is_successful=True, reason="", db=mock_save.call_args[0] if mock_save.call_args[0] else mock_outage.call_args[1]['db'])
    mock_breach_check.assert_called_once()
    mock_breach_handle.assert_called_once()


@patch('monitor.scheduler.perform_speed_test')
@patch('monitor.scheduler.get_public_ip')
@patch('monitor.scheduler.db.save_check_results')
@patch('monitor.scheduler.handle_outage_state')
def test_perform_network_check_failure(
    mock_outage,
    mock_save,
    mock_ip,
    mock_speedtest
):
    """Failed check should persist failure details and invoke outage handler."""
    mock_speedtest.return_value = {
        "download_speed": None,
        "upload_speed": None,
        "latency": None,
        "error": "No internet connection"
    }
    mock_ip.return_value = None

    perform_network_check()

    mock_save.assert_called_once()
    saved_kwargs = mock_save.call_args[1]
    assert saved_kwargs['download_speed'] is None
    assert saved_kwargs['status'] == 'failed'
    assert saved_kwargs['error'] == "No internet connection"

    mock_outage.assert_called_once()
    assert mock_outage.call_args[1]['is_successful'] is False
