// Copyright placeholder header comment.
package com.foo;

/** Bar is the primary type. */
@Deprecated
public class Bar<T> {
    private int count;
    protected String name;

    /** Builds a Bar. */
    public Bar(int count) {
        this.count = count;
    }

    /** Returns the count. */
    @Override
    public int getCount() {
        return count;
    }

    static final double RATIO = 1.5;

    interface Inner {
        void ping();
    }

    public static void main(String[] args) {
        System.out.println("hi");
    }
}

enum Color { RED, GREEN }
