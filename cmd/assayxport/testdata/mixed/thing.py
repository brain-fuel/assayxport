"""A simple thing module for testing polyglot extraction."""


class Thing:
    """Represents a generic thing."""

    def __init__(self, name: str) -> None:
        self.name = name

    def greet(self) -> str:
        """Return a greeting string."""
        return f"Hello, {self.name}!"
