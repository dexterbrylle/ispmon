#!/usr/bin/env python3
"""ISP Speed Monitor Entry Point."""

from monitor.db import init_db
from monitor.scheduler import setup_schedule, run_loop


def main():
    init_db()
    setup_schedule()
    run_loop()


if __name__ == "__main__":
    main()
