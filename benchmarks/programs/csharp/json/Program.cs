using System.Text;

const int N = 5_000_000;
using var stdout = Console.OpenStandardOutput();
using var writer = new StreamWriter(stdout, new UTF8Encoding(false), 1 << 20);

for (int i = 0; i < N; i++)
{
    writer.Write("{\"id\":");
    writer.Write(i);
    writer.Write(",\"name\":\"alice\",\"active\":true,\"score\":3.14}\n");
}

writer.Flush();
