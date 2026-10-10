import os
import pytest
from unittest.mock import patch, Mock
from monitor.reports import send_email_report, get_report_data


@pytest.fixture
def sample_report_data():
    return {
        'summary': {
            'Average Download Speed': '495.00 Mbps',
            'Average Upload Speed': '480.00 Mbps',
            'Average Latency': '12.00 ms',
            'IP Changes': 'No',
            'Slow Download Incidents': 0,
            'Slow Upload Incidents': 0,
            'Connection Outages': 0
        },
        'incidents': [],
        'raw_data': [
            ['2026-10-10 12:00:00', 495.0, 480.0, 12.0, '1.2.3.4']
        ]
    }


@pytest.fixture
def mock_smtp():
    with patch('smtplib.SMTP') as mock_smtp_cls:
        instance = mock_smtp_cls.return_value
        instance.__enter__.return_value = instance
        yield instance


@patch('monitor.reports.get_report_data')
def test_send_email_report_success(mock_get_data, mock_smtp, sample_report_data):
    """Test successful email report generation and dispatch."""
    mock_get_data.return_value = sample_report_data

    send_email_report(6)

    mock_smtp.starttls.assert_called_once()
    mock_smtp.login.assert_called_once()
    mock_smtp.send_message.assert_called_once()

    sent_msg = mock_smtp.send_message.call_args[0][0]
    assert 'Network Monitoring Report' in sent_msg['Subject']

    attachments = [part.get_filename() for part in sent_msg.get_payload() if part.get_filename()]
    assert any(name.endswith('.pdf') for name in attachments)
    assert any(name.endswith('.csv') for name in attachments)


@patch('monitor.reports.get_report_data')
def test_send_email_report_no_data(mock_get_data, mock_smtp):
    """When no records exist for period, skip sending email."""
    mock_get_data.return_value = None

    with patch('monitor.reports.logger.warning') as mock_warn:
        send_email_report(6)
        mock_warn.assert_called_once()
        mock_smtp.send_message.assert_not_called()


@patch('monitor.reports.get_report_data')
def test_send_email_report_smtp_error(mock_get_data, mock_smtp, sample_report_data):
    """Ensure SMTP connection failure is gracefully logged."""
    mock_get_data.return_value = sample_report_data
    mock_smtp.send_message.side_effect = Exception("SMTP Connection Refused")

    with patch('monitor.reports.logger.error') as mock_err:
        send_email_report(6)
        mock_err.assert_called_once()
