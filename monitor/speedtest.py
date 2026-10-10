import json
import logging
import shutil
import subprocess
import requests

logger = logging.getLogger(__name__)


def run_ookla_cli():
    """Execute official Ookla speedtest CLI binary if available.
    Returns dict of metrics or None if not installed / failed.
    """
    binary = shutil.which("speedtest")
    if not binary:
        return None

    try:
        cmd = [binary, "--format=json", "--accept-license", "--accept-gdpr"]
        proc = subprocess.run(cmd, capture_output=True, text=True, timeout=90)
        if proc.returncode != 0:
            logger.warning(f"Ookla CLI exited with code {proc.returncode}: {proc.stderr}")
            return None

        data = json.loads(proc.stdout)
        
        # Ookla returns bandwidth in bytes per second
        download_bytes = data.get("download", {}).get("bandwidth", 0)
        upload_bytes = data.get("upload", {}).get("bandwidth", 0)

        download_mbps = (download_bytes * 8) / 1_000_000
        upload_mbps = (upload_bytes * 8) / 1_000_000
        latency = data.get("ping", {}).get("latency")
        jitter = data.get("ping", {}).get("jitter")
        packet_loss = data.get("packetLoss")
        server_name = data.get("server", {}).get("name")
        result_url = data.get("result", {}).get("url")

        return {
            "download_speed": download_mbps,
            "upload_speed": upload_mbps,
            "latency": latency,
            "jitter": jitter,
            "packet_loss": packet_loss,
            "server_name": server_name,
            "result_url": result_url,
            "engine": "ookla-cli"
        }
    except Exception as e:
        logger.warning(f"Ookla CLI execution failed, falling back to python speedtest-cli: {e}")
        return None


def run_python_speedtest():
    """Execute python speedtest-cli library as fallback."""
    import speedtest
    st = speedtest.Speedtest()
    st.get_best_server()

    download_mbps = st.download() / 1_000_000
    upload_mbps = st.upload(pre_allocate=False) / 1_000_000
    latency = st.results.ping
    server_name = st.results.server.get('name') if getattr(st.results, 'server', None) else None

    return {
        "download_speed": download_mbps,
        "upload_speed": upload_mbps,
        "latency": latency,
        "jitter": None,
        "packet_loss": None,
        "server_name": server_name,
        "result_url": None,
        "engine": "speedtest-cli"
    }


def perform_speed_test():
    """Perform a speed test using Ookla CLI if available, else python library.
    Returns (download_speed, upload_speed, latency) for backward compatibility,
    and extra metrics dict.
    """
    try:
        res = run_ookla_cli()
        if not res:
            res = run_python_speedtest()

        return res
    except Exception as e:
        logger.error(f"Speed test error: {e}")
        return {
            "download_speed": None,
            "upload_speed": None,
            "latency": None,
            "jitter": None,
            "packet_loss": None,
            "server_name": None,
            "result_url": None,
            "error": str(e),
            "engine": "none"
        }


def get_public_ip():
    """Retrieve public IP address with timeout and fallback provider."""
    providers = [
        "https://ipinfo.io/ip",
        "https://api.ipify.org"
    ]
    for url in providers:
        try:
            resp = requests.get(url, timeout=10)
            if resp.status_code == 200:
                return resp.text.strip()
        except Exception as e:
            logger.debug(f"Public IP check via {url} failed: {e}")
            continue

    logger.error("All public IP lookups failed")
    return None
