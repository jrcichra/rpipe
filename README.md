# rpipe

Resilient pipes over HTTP.

### How it works

`rpipe` takes stdin and forwards the binary data wrapped in HTTP/1.1 chunks over the wire to a command on the other end. It allows you to specify the command to run on the other side. That command's stdin will be hooked into the pipe. For example, to copy a file over `rpipe`:

```bash
cat data | rpipe --url https://rpipe.jrcichra.dev --headers "CF-Access-Client-Id=abc.access,CF-Access-Client-Secret=123" --command 'cat >> data'
```

You can also use it for more complex pipelines, such as including lz4 or processing with ffmpeg.

Using HTTP enables the easy use of zero trust frameworks like [Cloudflare Access](https://www.cloudflare.com/products/zero-trust/access/).

When the network connection is broken, the client retries sending the chunk until it's successful.

The goal is to have a resilient, flexible, never give up tool that wouldn't care if your client went missing for a week (assuming both systems didn't reboot).

### Getting started

`cargo build --release` will provide a copy of `rpipe` and `rpiped` for your system.

You can also extract binaries from the container image built on each commit.

# Bugs

There is an intermittent issue where a chunk of the data is accepted and forwarded to the server process but not acknowledged by the client. This might be due to something like Cloudflare successfully giving the backend all the data but the response doesn't make it to my client intact.

For now I've made it so the stream tracks the progress and will fail if there's a mismatch in position. But this is something which will be investigated further as time goes on. A potential remediation might be to recognize a chunk jump and skip ahead on the client.
