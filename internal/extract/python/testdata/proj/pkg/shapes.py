def constant(x):
    return x + 1

def linear(xs):
    total = 0
    for x in xs:
        total += x
    return total

def quadratic(xs):
    n = 0
    for a in xs:
        for b in xs:
            n += 1
    return n

def collect(xs):
    out = []
    for x in xs:
        out.append(x * 2)
    return out

def recur(n):
    if n <= 0:
        return 0
    return recur(n - 1)

def closure(xs):
    # The loop lives in a nested def and must NOT count toward closure (O(1)).
    def inner():
        s = 0
        for x in xs:
            s += x
        return s
    return inner()

def nested_class(xs):
    # A loop at a nested class body must NOT count toward nested_class (O(1)).
    class C:
        for x in xs:
            pass
    return C

def get_header_value(k):
    return k

class Evt:
    @property  # type: ignore[override]
    def prop_c(self):
        return 1

    x = y = z = _sentinel

    def walk(self):
        # self.walk() is a genuine self-call -> recursive.
        return self.walk()

    def get_header_value(self, k):
        # Bare name matches this method's name but resolves to the module-level
        # free function, NOT self, so it must NOT be flagged recursive.
        return get_header_value(k)

def dict_build(xs):
    # Dict comprehension: O(n) time AND O(n) space.
    return {k: k for k in xs}

def dict_loop(xs):
    # Subscript assignment inside a loop grows a dict: O(n) space.
    out = {}
    for k in xs:
        out[k] = 1
    return out
