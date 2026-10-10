import csv
import io
import logging
from datetime import datetime, timezone, timedelta
import smtplib
from email.mime.text import MIMEText
from email.mime.multipart import MIMEMultipart
from email.mime.application import MIMEApplication

from reportlab.lib import colors
from reportlab.lib.pagesizes import letter
from reportlab.platypus import SimpleDocTemplate, Paragraph, Spacer, Table, TableStyle
from reportlab.lib.styles import getSampleStyleSheet

from monitor.config import (
    SMTP_SERVER,
    SMTP_PORT,
    SMTP_USER,
    SMTP_PASSWORD,
    EMAIL_RECIPIENT,
    SPEED_ALERT_DOWNLOAD,
    SPEED_ALERT_UPLOAD,
    is_email_configured
)
from monitor.db import get_report_data as db_get_report_data

logger = logging.getLogger(__name__)


def _format_timestamp(ts):
    """Safely format timestamp whether it is a string or datetime object."""
    if not ts:
        return ""
    if isinstance(ts, datetime):
        return ts.strftime('%Y-%m-%d %H:%M:%S')
    try:
        dt = datetime.fromisoformat(str(ts))
        return dt.strftime('%Y-%m-%d %H:%M:%S')
    except Exception:
        return str(ts)[:19]


def generate_pdf_report(data, start_time, end_time, buffer_or_filename):
    """Generate a PDF report using ReportLab."""
    doc = SimpleDocTemplate(buffer_or_filename, pagesize=letter)
    styles = getSampleStyleSheet()
    elements = []

    elements.append(Paragraph("Network Monitoring Report", styles['Title']))
    elements.append(Spacer(1, 12))

    period_text = f"Report Period: {start_time.strftime('%m/%d/%Y, %I:%M:%S %p')} - {end_time.strftime('%m/%d/%Y, %I:%M:%S %p')}"
    elements.append(Paragraph(period_text, styles['Normal']))
    elements.append(Spacer(1, 12))

    elements.append(Paragraph("Summary:", styles['Heading2']))
    for key, value in data['summary'].items():
        elements.append(Paragraph(f"• {key}: {value}", styles['Normal']))
    elements.append(Spacer(1, 12))

    if data.get('incidents'):
        elements.append(Paragraph("Speed Incidents:", styles['Heading2']))
        table_data = [['Timestamp', 'Download Speed', 'Upload Speed']]
        table_data.extend(data['incidents'])

        t = Table(table_data)
        t.setStyle(TableStyle([
            ('BACKGROUND', (0, 0), (-1, 0), colors.grey),
            ('TEXTCOLOR', (0, 0), (-1, 0), colors.whitesmoke),
            ('ALIGN', (0, 0), (-1, -1), 'CENTER'),
            ('FONTNAME', (0, 0), (-1, 0), 'Helvetica-Bold'),
            ('FONTSIZE', (0, 0), (-1, 0), 12),
            ('BOTTOMPADDING', (0, 0), (-1, 0), 10),
            ('TEXTCOLOR', (0, 1), (-1, -1), colors.black),
            ('FONTNAME', (0, 1), (-1, -1), 'Helvetica'),
            ('FONTSIZE', (0, 1), (-1, -1), 10),
            ('GRID', (0, 0), (-1, -1), 1, colors.black)
        ]))
        elements.append(t)

    doc.build(elements)


def generate_csv_report(data, buffer_or_filename):
    """Generate CSV report of raw check rows."""
    if hasattr(buffer_or_filename, 'write'):
        writer = csv.writer(buffer_or_filename)
        writer.writerow(['Timestamp', 'Download Speed (Mbps)', 'Upload Speed (Mbps)', 'Latency (ms)', 'IP Address'])
        for row in data:
            writer.writerow(row)
    else:
        with open(buffer_or_filename, 'w', newline='') as f:
            writer = csv.writer(f)
            writer.writerow(['Timestamp', 'Download Speed (Mbps)', 'Upload Speed (Mbps)', 'Latency (ms)', 'IP Address'])
            for row in data:
                writer.writerow(row)


def get_report_data(hours):
    """Get summarized report data for the specified number of hours."""
    results = db_get_report_data(hours)
    if not results:
        return None

    download_speeds = [r['download_speed'] for r in results if r.get('download_speed') is not None]
    upload_speeds = [r['upload_speed'] for r in results if r.get('upload_speed') is not None]
    latencies = [r['latency'] for r in results if r.get('latency') is not None]
    ip_addresses = set(r['ip_address'] for r in results if r.get('ip_address'))
    outages = sum(1 for r in results if r.get('status') != 'ok')

    summary = {
        'Average Download Speed': f"{sum(download_speeds)/len(download_speeds):.2f} Mbps" if download_speeds else "N/A",
        'Average Upload Speed': f"{sum(upload_speeds)/len(upload_speeds):.2f} Mbps" if upload_speeds else "N/A",
        'Average Latency': f"{sum(latencies)/len(latencies):.2f} ms" if latencies else "N/A",
        'IP Changes': 'Yes' if len(ip_addresses) > 1 else 'No',
        'Slow Download Incidents': sum(1 for s in download_speeds if s < SPEED_ALERT_DOWNLOAD),
        'Slow Upload Incidents': sum(1 for s in upload_speeds if s < SPEED_ALERT_UPLOAD),
        'Connection Outages': outages
    }

    incidents = []
    for r in results:
        dl = r.get('download_speed')
        ul = r.get('upload_speed')
        if dl is not None and ul is not None:
            if dl < SPEED_ALERT_DOWNLOAD or ul < SPEED_ALERT_UPLOAD:
                incidents.append([
                    _format_timestamp(r.get('timestamp')),
                    f"{dl:.2f} Mbps" if dl < SPEED_ALERT_DOWNLOAD else "OK",
                    f"{ul:.2f} Mbps" if ul < SPEED_ALERT_UPLOAD else "OK"
                ])

    raw_data = [
        [
            _format_timestamp(r.get('timestamp')),
            r.get('download_speed'),
            r.get('upload_speed'),
            r.get('latency'),
            r.get('ip_address')
        ]
        for r in results
    ]

    return {
        'summary': summary,
        'incidents': incidents,
        'raw_data': raw_data
    }


def send_email_report(hours):
    """Send scheduled email report with PDF and CSV attachments."""
    if not is_email_configured():
        logger.debug("Email report skipped — email not configured")
        return

    try:
        end_time = datetime.now()
        start_time = end_time - timedelta(hours=hours)

        data = get_report_data(hours)
        if not data:
            logger.warning(f"No data available for the past {hours} hours")
            return

        # Generate in-memory PDF
        pdf_buffer = io.BytesIO()
        generate_pdf_report(data, start_time, end_time, pdf_buffer)
        pdf_bytes = pdf_buffer.getvalue()

        # Generate in-memory CSV
        csv_buffer = io.StringIO()
        generate_csv_report(data['raw_data'], csv_buffer)
        csv_bytes = csv_buffer.getvalue().encode('utf-8')

        msg = MIMEMultipart()
        msg['Subject'] = f"Network Monitoring Report ({hours}h)"
        msg['From'] = SMTP_USER
        msg['To'] = EMAIL_RECIPIENT

        body = f"""Network Monitoring Report
Report Period: {start_time.strftime('%m/%d/%Y, %I:%M:%S %p')} - {end_time.strftime('%m/%d/%Y, %I:%M:%S %p')}

Summary:
"""
        for key, value in data['summary'].items():
            body += f"• {key}: {value}\n"

        msg.attach(MIMEText(body))

        # Attach PDF
        pdf_filename = f"network_report_{hours}h.pdf"
        pdf_attachment = MIMEApplication(pdf_bytes, _subtype='pdf')
        pdf_attachment.add_header('Content-Disposition', 'attachment', filename=pdf_filename)
        msg.attach(pdf_attachment)

        # Attach CSV
        csv_filename = f"network_data_{hours}h.csv"
        csv_attachment = MIMEApplication(csv_bytes, _subtype='csv')
        csv_attachment.add_header('Content-Disposition', 'attachment', filename=csv_filename)
        msg.attach(csv_attachment)

        with smtplib.SMTP(SMTP_SERVER, SMTP_PORT) as server:
            server.starttls()
            server.login(SMTP_USER, SMTP_PASSWORD)
            server.send_message(msg)

        logger.info(f"Email report sent successfully for past {hours} hours")
    except Exception as e:
        logger.error(f"Error sending email report: {e}")
