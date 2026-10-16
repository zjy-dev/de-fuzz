"""Entrypoint for python -m defuzz_loop.cli."""

import sys
from defuzz_loop.cli.main import main

if __name__ == "__main__":
    sys.exit(main())
