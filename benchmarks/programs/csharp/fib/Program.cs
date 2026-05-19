static long Fib(long n)
{
    if (n < 2) return n;
    return Fib(n - 1) + Fib(n - 2);
}

Console.WriteLine(Fib(40));
