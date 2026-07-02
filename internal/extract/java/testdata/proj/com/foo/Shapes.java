package com.foo;

import java.util.ArrayList;
import java.util.List;

public class Shapes {
    public int constant(int x) {
        return x + 1;
    }

    public int linear(int[] xs) {
        int total = 0;
        for (int x : xs) {
            total += x;
        }
        return total;
    }

    public int quadratic(int[] xs) {
        int n = 0;
        for (int a : xs) {
            for (int b : xs) {
                n++;
            }
        }
        return n;
    }

    public List<Integer> collect(int[] xs) {
        List<Integer> out = new ArrayList<>();
        for (int x : xs) {
            out.add(x * 2);
        }
        return out;
    }

    public int recur(int n) {
        if (n <= 0) {
            return 0;
        }
        return recur(n - 1);
    }

    // noLoopHere: its only loop is inside a lambda body and must not inflate
    // this method's complexity. Regression guard for nested-scope skipping.
    public void noLoopHere(int[] xs) {
        Runnable r = () -> {
            for (int x : xs) {
                System.out.println(x);
            }
        };
        r.run();
    }

    // Overload delegation: delegate(int) calls delegate(int, int). Different
    // arity, so it must NOT be flagged recursive (name-only matching did).
    public int delegate(int x) {
        return delegate(x, 0);
    }

    public int delegate(int x, int y) {
        return x + y;
    }

    // Nested enum exercises enum-body member extraction: the constructor,
    // field, and method inside the enum body must be emitted, not dropped.
    public enum Kind {
        A {
            public int label() {
                return 1;
            }
        },
        B;

        private final int code;

        Kind() {
            this.code = 0;
        }

        public int getCode() {
            return this.code;
        }
    }

    // Static nested class: its static/final modifiers must appear in the type
    // symbol's signature.modifiers.
    static final class Nested {
    }
}
