"""Sample module for extractor tests."""

__all__ = ["Widget", "make"]


def make(name: str, size: int = 1) -> "Widget":
    """Build a Widget."""
    return Widget(name)


def _helper(x):
    return x


async def fetch(url):
    """Fetch a url."""
    return url


class Widget:
    """A widget."""

    count = 0

    def __init__(self, name: str):
        """Init."""
        self.name = name

    @property
    def label(self) -> str:
        return self.name

    def _private(self):
        return None


class Outer:
    """Outer class."""

    class Inner:
        """Inner class."""

        def ping(self):
            """Ping."""
            return "pong"


def documented():
    # leading comment before the docstring
    """Documented via comment-preceded docstring."""
    return 1


if __name__ == "__main__":
    make("x")
