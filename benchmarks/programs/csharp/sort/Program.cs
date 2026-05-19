const int N = 64;
const int REPS = 50_000;
const long MOD = 2_147_483_647L;

static long Next(ref long state)
{
    state = (state * 1_103_515_245L + 12_345L) % MOD;
    return state;
}

var a = new long[N];
long state = 123_456_789L;
long chk = 0;

for (int rep = 0; rep < REPS; rep++)
{
    for (int i = 0; i < N; i++)
    {
        a[i] = Next(ref state);
    }

    for (int i = 1; i < N; i++)
    {
        long key = a[i];
        int j = i - 1;
        while (j >= 0 && a[j] > key)
        {
            a[j + 1] = a[j];
            j--;
        }
        a[j + 1] = key;
    }

    for (int i = 0; i < N; i++)
    {
        chk = (chk + a[i] + i) % MOD;
    }
}

Console.WriteLine(chk);
