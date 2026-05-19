using System.Net;
using System.Net.Sockets;
using System.Text;

string host = args.Length > 0 ? args[0] : "127.0.0.1";
int port = args.Length > 1 ? int.Parse(args[1]) : 8080;

var listener = new TcpListener(IPAddress.Parse(host), port);
listener.Server.SetSocketOption(SocketOptionLevel.Socket, SocketOptionName.ReuseAddress, true);
listener.Start(1024);
Console.Error.WriteLine($"csharp(dynamic): listening on {host}:{port}");

var svc = new Service("hello\n");

while (true)
{
    var client = await listener.AcceptTcpClientAsync();
    _ = Task.Run(async () =>
    {
        try
        {
            using (client)
            {
                client.NoDelay = true;
                var stream = client.GetStream();
                var buf = new byte[4096];
                while (true)
                {
                    int n = await stream.ReadAsync(buf, 0, buf.Length);
                    if (n <= 0) break;

                    int reqs = 0;
                    for (int i = 3; i < n; i++)
                    {
                        if (buf[i - 3] == '\r' && buf[i - 2] == '\n' && buf[i - 1] == '\r' && buf[i] == '\n')
                        {
                            reqs++;
                        }
                    }
                    if (reqs == 0) reqs = 1;

                    for (int i = 0; i < reqs; i++)
                    {
                        var body = Handle(svc);
                        var response = BuildResponse(body);
                        await stream.WriteAsync(response, 0, response.Length);
                    }
                }
            }
        }
        catch
        {
            // Connection closed / benchmark harness kill.
        }
    });
}

static byte[] BuildResponse(string body)
{
    return Encoding.ASCII.GetBytes(
        "HTTP/1.1 200 OK\r\n" +
        "Content-Type: text/plain\r\n" +
        $"Content-Length: {Encoding.ASCII.GetByteCount(body)}\r\n" +
        "Connection: keep-alive\r\n\r\n" +
        body
    );
}

static string Handle(Service svc) => svc.Body;

sealed class Service
{
    public string Body { get; }

    public Service(string body)
    {
        Body = body;
    }
}
