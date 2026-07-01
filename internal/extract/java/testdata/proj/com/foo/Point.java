package com.foo;

/** A value record for a 2-D point. */
public record Point(int x, int y) {}

/** Custom marker annotation. */
@interface Marker {
    String value();
}

/** Logger with implements and varargs. */
class Logger implements Runnable {
    public void run() {}
    public void log(String... args) {}
    public <T extends Comparable<T>> void sortIt(T a) {}
}
