import csv
import io
import logging
from datetime import datetime, timezone, timedelta
from email.mime.text import MIMEText
from email.mime.multipart import MIMEMultipart
from email.mime.application import MIMEApplication

import requests
import smtplib

from monitor.config import (
    SMTP_SERVER,
    SMTP_PORT,
    SMTP_USER,
    SMTP_PASSWORD,
    EMAIL_RECIPIENT,
    DISCORD_WEBHOOK_URL,
    ISP_PLAN_NAME,
    is_email_configured,
    is_discord_configured
)

logger = logging.getLogger(__name__)


def send_discord_alert(result):
    """Send a compact SLA breach alert card to Discord."""
    if not is_discord_configured():
        return

    try:
        worst_down = f"{result['worst_download']:.1f}" if result.get('worst_download') else "N/A"
        worst_up = f"{result['worst_upload']:.1f}" if result.get('worst_upload') else "N/A"

        fields = [
            {"name": "Below Threshold", "value": f"{result['violating_count']}/{result['total_count']} checks", "inline": True},
            {"name": "Violation %", "value": f"{result['violation_pct']}%", "inline": True},
            {"name": "Contractual Minimum", "value": f"{result.get('threshold', 150)} Mbps", "inline": True},
            {"name": "Worst Download", "value": f"{worst_down} Mbps", "inline": True},
            {"name": "Worst Upload", "value": f"{worst_up} Mbps", "inline": True},
        ]

        # If any check has an official Ookla result URL, attach it
        urls = [c.get('result_url') for c in result.get('breached_checks', []) if c.get('result_url')]
        if urls:
            fields.append({"name": "Latest Ookla Proof", "value": f"[View Speedtest Result]({urls[-1]})", "inline": False})

        embed = {
            "title": f"⚠️ ISP SLA Breach Declared — {result.get('plan_name', ISP_PLAN_NAME)}",
            "description": "Connection speed dropped below contractual commitment for >20% of checks in the rolling window.",
            "color": 0xD32F2F,  # Red
            "fields": fields,
            "timestamp": datetime.now(timezone.utc).isoformat()
        }

        resp = requests.post(DISCORD_WEBHOOK_URL, json={"embeds": [embed]}, timeout=10)
        resp.raise_for_status()
        logger.info("Discord SLA breach alert sent successfully")
    except Exception as e:
        logger.error(f"Failed to send Discord alert: {e}")


def send_discord_resolved(active_episode):
    """Send a recovery notification to Discord when breach clears."""
    if not is_discord_configured():
        return

    try:
        started_str = active_episode.get('started_at', 'N/A')
        duration_desc = "Unknown"
        if started_str != 'N/A':
            try:
                start_dt = datetime.fromisoformat(started_str)
                secs = int((datetime.now(timezone.utc) - start_dt).total_seconds())
                hours = secs // 3600
                mins = (secs % 3600) // 60
                duration_desc = f"{hours}h {mins}m"
            except Exception:
                duration_desc = started_str

        embed = {
            "title": f"✅ ISP SLA Breach Resolved — {ISP_PLAN_NAME}",
            "description": "Connection speeds have stabilized back within contractual SLA standards.",
            "color": 0x388E3C,  # Green
            "fields": [
                {"name": "Breach Episode ID", "value": str(active_episode.get('id', 'N/A')), "inline": True},
                {"name": "Total Breach Duration", "value": duration_desc, "inline": True},
                {"name": "Peak Violation %", "value": f"{active_episode.get('violation_pct', 'N/A')}%", "inline": True},
            ],
            "timestamp": datetime.now(timezone.utc).isoformat()
        }

        resp = requests.post(DISCORD_WEBHOOK_URL, json={"embeds": [embed]}, timeout=10)
        resp.raise_for_status()
        logger.info("Discord breach resolution alert sent")
    except Exception as e:
        logger.error(f"Failed to send Discord resolution alert: {e}")


def send_discord_outage_alert(reason: str):
    """Send an outage notification to Discord."""
    if not is_discord_configured():
        return

    try:
        embed = {
            "title": f"🚨 Network Outage Detected — {ISP_PLAN_NAME}",
            "description": f"Connection is completely down or unreachable.\nReason: `{reason}`",
            "color": 0xE65100,  # Deep orange
            "timestamp": datetime.now(timezone.utc).isoformat()
        }
        resp = requests.post(DISCORD_WEBHOOK_URL, json={"embeds": [embed]}, timeout=10)
        resp.raise_for_status()
        logger.info("Discord outage alert sent")
    except Exception as e:
        logger.error(f"Failed to send Discord outage alert: {e}")


def send_discord_outage_resolved(duration_seconds: int):
    """Send a restored notification to Discord."""
    if not is_discord_configured():
        return

    try:
        mins = duration_seconds // 60
        secs = duration_seconds % 60
        embed = {
            "title": f"🌐 Connection Restored — {ISP_PLAN_NAME}",
            "description": f"Internet connectivity has been restored after **{mins}m {secs}s**.",
            "color": 0x1976D2,  # Blue
            "timestamp": datetime.now(timezone.utc).isoformat()
        }
        resp = requests.post(DISCORD_WEBHOOK_URL, json={"embeds": [embed]}, timeout=10)
        resp.raise_for_status()
        logger.info("Discord outage restored alert sent")
    except Exception as e:
        logger.error(f"Failed to send Discord outage restored alert: {e}")


def send_breach_email(result):
    """Send HTML breach evidence email with in-memory CSV attachment."""
    if not is_email_configured():
        return

    try:
        end_time = datetime.now()
        start_time = end_time - timedelta(hours=24)
        worst_down = f"{result['worst_download']:.1f}" if result.get('worst_download') else "N/A"
        worst_up = f"{result['worst_upload']:.1f}" if result.get('worst_upload') else "N/A"
        threshold = result.get('threshold', 150)
        plan_name = result.get('plan_name', ISP_PLAN_NAME)

        # Build in-memory CSV
        csv_buffer = io.StringIO()
        writer = csv.writer(csv_buffer)
        writer.writerow([
            'Timestamp', 'Download Speed (Mbps)', 'Upload Speed (Mbps)',
            'Latency (ms)', 'IP Address', 'Server', 'Ookla URL', 'Below Threshold', 'Status'
        ])
        for check in result.get('breached_checks', []):
            writer.writerow([
                check.get('timestamp', ''),
                check.get('download_speed', ''),
                check.get('upload_speed', ''),
                check.get('latency', ''),
                check.get('ip_address', ''),
                check.get('server_name', ''),
                check.get('result_url', ''),
                check.get('below_threshold', ''),
                check.get('status', '')
            ])
        csv_bytes = csv_buffer.getvalue().encode('utf-8')

        html_body = f"""
        <html>
        <body style="font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, Helvetica, Arial, sans-serif; color: #333; line-height: 1.5;">
            <div style="max-width: 600px; margin: 0 auto; padding: 20px; border: 1px solid #e0e0e0; border-radius: 8px;">
                <h2 style="color: #c62828; margin-top: 0;">ISP SLA Breach Detected — {plan_name}</h2>
                <p>Connection quality has breached your contractual SLA commitments over the last 24-hour evaluation window.</p>
                
                <table style="width: 100%; border-collapse: collapse; margin: 20px 0;">
                    <tr style="background: #f9f9f9; border-bottom: 2px solid #ddd;">
                        <th style="padding: 10px; text-align: left;">Metric</th>
                        <th style="padding: 10px; text-align: left;">Observed Value</th>
                    </tr>
                    <tr style="border-bottom: 1px solid #eee;">
                        <td style="padding: 10px;"><strong>Contract Minimum</strong></td>
                        <td style="padding: 10px;">{threshold} Mbps</td>
                    </tr>
                    <tr style="border-bottom: 1px solid #eee;">
                        <td style="padding: 10px;"><strong>Checks Below SLA</strong></td>
                        <td style="padding: 10px;">{result['violating_count']} / {result['total_count']} valid checks</td>
                    </tr>
                    <tr style="border-bottom: 1px solid #eee;">
                        <td style="padding: 10px;"><strong>Violation Rate</strong></td>
                        <td style="padding: 10px; color: #c62828; font-weight: bold;">{result['violation_pct']}% (Rule: >20%)</td>
                    </tr>
                    <tr style="border-bottom: 1px solid #eee;">
                        <td style="padding: 10px;"><strong>Lowest Download</strong></td>
                        <td style="padding: 10px;">{worst_down} Mbps</td>
                    </tr>
                    <tr style="border-bottom: 1px solid #eee;">
                        <td style="padding: 10px;"><strong>Lowest Upload</strong></td>
                        <td style="padding: 10px;">{worst_up} Mbps</td>
                    </tr>
                </table>

                <p style="font-size: 0.9em; color: #666;">
                    Attached is the full CSV evidence log. Use this data when opening a ticket with your ISP.
                </p>
            </div>
        </body>
        </html>
        """

        msg = MIMEMultipart()
        msg['Subject'] = f"ISP SLA Breach — {plan_name}"
        msg['From'] = SMTP_USER
        msg['To'] = EMAIL_RECIPIENT

        msg.attach(MIMEText(html_body, 'html'))

        filename = f"sla_breach_evidence_{end_time.strftime('%Y%m%d_%H%M%S')}.csv"
        csv_attachment = MIMEApplication(csv_bytes, _subtype='csv')
        csv_attachment.add_header('Content-Disposition', 'attachment', filename=filename)
        msg.attach(csv_attachment)

        with smtplib.SMTP(SMTP_SERVER, SMTP_PORT) as server:
            server.starttls()
            server.login(SMTP_USER, SMTP_PASSWORD)
            server.send_message(msg)

        logger.info("Breach email sent successfully with in-memory CSV evidence")
    except Exception as e:
        logger.error(f"Failed to send breach email: {e}")
