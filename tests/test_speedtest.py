import json
from unittest.mock import patch, Mock
from monitor.speedtest import run_ookla_cli, run_python_speedtest, perform_speed_test, get_public_ip


def test_run_ookla_cli_success():
    """Verify Ookla CLI JSON parsing and conversion from bytes/sec to Mbps."""
    mock_payload = {
        "download": {"bandwidth": 62500000},  # 62.5 MB/s = 500 Mbps
        "upload": {"bandwidth": 62500000},    # 500 Mbps
        "ping": {"latency": 8.5, "jitter": 1.2},
        "packetLoss": 0.0,
        "server": {"name": "Test Server"},
        "result": {"url": "https://www.speedtest.net/result/c/123"}
    }

    mock_proc = Mock()
    mock_proc.returncode = 0
    mock_proc.stdout = json.dumps(mock_payload)

    with patch('shutil.which', return_value='/usr/bin/speedtest'), \
         patch('subprocess.run', return_value=mock_proc):
        res = run_ookla_cli()

        assert res is not None
        assert res['download_speed'] == 500.0
        assert res['upload_speed'] == 500.0
        assert res['latency'] == 8.5
        assert res['jitter'] == 1.2
        assert res['packet_loss'] == 0.0
        assert res['server_name'] == "Test Server"
        assert res['result_url'] == "https://www.speedtest.net/result/c/123"
        assert res['engine'] == "ookla-cli"


def test_run_ookla_cli_not_installed():
    """When Ookla CLI binary is missing, return None."""
    with patch('shutil.which', return_value=None):
        assert run_ookla_cli() is None


def test_fallback_to_python_speedtest():
    """When Ookla CLI is unavailable, fallback smoothly to python speedtest-cli."""
    with patch('monitor.speedtest.run_ookla_cli', return_value=None), \
         patch('monitor.speedtest.run_python_speedtest') as mock_fallback:
        mock_fallback.return_value = {
            "download_speed": 100.0,
            "upload_speed": 50.0,
            "latency": 20.0,
            "engine": "speedtest-cli"
        }
        res = perform_speed_test()
        assert res['download_speed'] == 100.0
        assert res['engine'] == "speedtest-cli"


def test_get_public_ip_success():
    """Verify public IP lookup with mocked HTTP request."""
    mock_resp = Mock()
    mock_resp.status_code = 200
    mock_resp.text = "104.28.19.45\n"

    with patch('requests.get', return_value=mock_resp):
        ip = get_public_ip()
        assert ip == "104.28.19.45"


def test_get_public_ip_fallback():
    """Verify fallback to second provider when first fails."""
    mock_ok = Mock()
    mock_ok.status_code = 200
    mock_ok.text = "104.28.19.45"

    with patch('requests.get', side_effect=[Exception("Timeout"), mock_ok]):
        ip = get_public_ip()
        assert ip == "104.28.19.45"
