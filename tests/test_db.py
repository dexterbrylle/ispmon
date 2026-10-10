import os
from datetime import datetime, timezone
from monitor.db import (
    init_db,
    save_check_results,
    get_recent_checks,
    get_check_count,
    get_report_data,
    start_breach_episode,
    update_breach_episode,
    end_breach_episode,
    get_active_breach_episode,
    start_outage_episode,
    end_outage_episode,
    get_active_outage_episode
)


def test_init_db_creates_tables(test_db_path):
    """Ensure database initialization creates expected SQLite tables."""
    assert os.path.exists(test_db_path)
    count = get_check_count(hours=24, db_path=test_db_path)
    assert count == 0


def test_save_and_retrieve_checks(test_db_path):
    """Verify saving check results and querying recent window."""
    save_check_results(
        download_speed=450.0,
        upload_speed=420.0,
        latency=15.0,
        ip_address='1.2.3.4',
        below_threshold=False,
        status='ok',
        server_name='Server A',
        result_url='https://speedtest.net/result/12345',
        db_path=test_db_path
    )

    checks = get_recent_checks(hours=1, db_path=test_db_path)
    assert len(checks) == 1
    c = checks[0]
    assert c['download_speed'] == 450.0
    assert c['upload_speed'] == 420.0
    assert c['latency'] == 15.0
    assert c['server_name'] == 'Server A'
    assert c['result_url'] == 'https://speedtest.net/result/12345'
    assert c['status'] == 'ok'

    report = get_report_data(hours=1, db_path=test_db_path)
    assert len(report) == 1


def test_sla_breach_episode_lifecycle(test_db_path):
    """Test full lifecycle of an SLA breach episode."""
    active = get_active_breach_episode(db_path=test_db_path)
    assert active is None

    ep_id = start_breach_episode(
        violating_count=10,
        total_count=48,
        violation_pct=20.8,
        worst_down=100.0,
        worst_up=95.0,
        db_path=test_db_path
    )
    assert ep_id is not None

    active = get_active_breach_episode(db_path=test_db_path)
    assert active is not None
    assert active['id'] == ep_id
    assert active['worst_download_speed'] == 100.0

    # Update with worse speeds
    update_breach_episode(
        episode_id=ep_id,
        worst_down=80.0,
        worst_up=70.0,
        violating_count=12,
        total_count=48,
        violation_pct=25.0,
        db_path=test_db_path
    )
    active = get_active_breach_episode(db_path=test_db_path)
    assert active['worst_download_speed'] == 80.0
    assert active['violating_check_count'] == 12

    # Close episode
    end_breach_episode(ep_id, db_path=test_db_path)
    active_after = get_active_breach_episode(db_path=test_db_path)
    assert active_after is None


def test_outage_episode_lifecycle(test_db_path):
    """Test full lifecycle of a connectivity outage episode."""
    active = get_active_outage_episode(db_path=test_db_path)
    assert active is None

    ep_id = start_outage_episode(reason="Gateway unreachable", db_path=test_db_path)
    assert ep_id is not None

    active = get_active_outage_episode(db_path=test_db_path)
    assert active is not None
    assert active['id'] == ep_id
    assert active['reason'] == "Gateway unreachable"

    duration = end_outage_episode(ep_id, db_path=test_db_path)
    assert duration is not None
    assert duration >= 0

    active_after = get_active_outage_episode(db_path=test_db_path)
    assert active_after is None
