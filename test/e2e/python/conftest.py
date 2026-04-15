import os


def pytest_configure(config):
    os.environ.setdefault("E2B_API_URL", "http://localhost:3000")
    os.environ.setdefault("E2B_DOMAIN", "localhost:3000")
    os.environ.setdefault("E2B_API_KEY", "edvabe_local")
