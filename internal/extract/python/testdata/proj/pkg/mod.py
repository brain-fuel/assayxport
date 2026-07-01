"""Module pkg.mod: main module."""

__all__ = ["main", "AsyncWorker"]

_private = "secret"


class AsyncWorker:
    """An async worker class."""

    count = 0

    def __init__(self):
        """Initialize worker."""
        pass

    @property
    def label(self):
        """Worker label."""
        return "worker"

    async def run(self):
        """Run the worker asynchronously."""
        pass


async def fetch(url: str) -> str:
    """Fetch a URL asynchronously."""
    pass


def main():
    """Entry point."""
    pass


if __name__ == "__main__":
    main()
