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
