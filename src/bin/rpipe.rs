use anyhow::{anyhow, Context};
use axum::http::HeaderValue;
use clap::Parser;
use log::info;
use reqwest::{
    header::{HeaderMap, HeaderName},
    StatusCode,
};
use rpipe::consts::{EXPECTED_SIZE_HEADER, JOB_ID_HEADER};
use std::{
    io::{BufReader, Read},
    thread,
    time::Duration,
};

#[derive(Parser, Debug)]
#[clap(author, version, about, long_about = None)]
struct Args {
    #[clap(long)]
    url: String,
    #[clap(long)]
    headers: Option<String>,
    #[clap(long)]
    command: String,
    #[clap( long,default_value_t= 1 * 1024 * 1024)]
    chunk_size: usize,
    #[clap(long, default_value_t = 4000)]
    backoff: u64,
}

#[tokio::main]
async fn main() -> Result<(), anyhow::Error> {
    simple_logger::init_with_level(log::Level::Info)?;
    let args = Args::parse();
    let create_url = format!("{}/create", args.url);
    let upload_url = format!("{}/upload", args.url);
    let done_url = format!("{}/done", args.url);

    // build headers from arg
    let mut additional_headers = HeaderMap::new();
    if let Some(headers) = args.headers {
        for header in headers.split(",") {
            let (key, value) = header
                .split_once("=")
                .context("could not find = separator in header")?;
            // from_bytes is needed here, otherwise static lifetime strings are required
            // https://users.rust-lang.org/t/add-dynamic-custom-headers-with-reqwest/87149/2
            additional_headers.insert(
                HeaderName::from_bytes(key.as_bytes())?,
                HeaderValue::from_bytes(value.as_bytes())?,
            );
        }
    }

    // attach to stdin
    let stdin = std::io::stdin();

    // build a client
    let client = reqwest::Client::builder()
        .default_headers(additional_headers)
        .user_agent("rpipe")
        .build()?;

    // create a new job with a command
    let resp = client.post(&create_url).body(args.command).send().await?;

    // make sure the request was successful
    if resp.status() != StatusCode::OK {
        return Err(anyhow!(
            "bad return code when making size. expected 200 OK, got {}. body:  {}",
            resp.status(),
            resp.text().await?
        ));
    }

    // the text of the response is the job id
    let job_id = resp.text().await?;
    if job_id.len() <= 0 {
        return Err(anyhow!("bad job id when creating",));
    }

    let mut reader = BufReader::new(stdin);

    // read chunks of stdin bytes
    loop {
        let mut buf = vec![0; args.chunk_size];
        let mut total_bytes = 0;
        loop {
            let bytes = reader.read(&mut buf[total_bytes..])?;
            total_bytes += bytes;
            if bytes <= 0 {
                // End of input
                break;
            }
        }

        if total_bytes <= 0 {
            info!("complete");
            break;
        }
        // Since we didn't use up the full buffer
        // we need to shorten buffer to just the size of
        // the data.
        // There's probably a better way of utilizing read
        // where this step wouldn't be necessary.
        if total_bytes < args.chunk_size {
            let mut b = Vec::with_capacity(total_bytes);
            b.extend(&buf[0..total_bytes]);
            buf = b;
        }

        // loop for every time a chunk errors out
        loop {
            info!("uploading chunk of length: {}...", total_bytes);
            // clone the buffer because body() moves the data
            let buf = buf.clone();
            let response_result = client
                .post(&upload_url)
                .body(buf)
                .header(EXPECTED_SIZE_HEADER, total_bytes)
                .header(JOB_ID_HEADER, &job_id)
                .send()
                .await;

            let resp = match response_result {
                Ok(data) => data,
                Err(e) => {
                    info!("uploaded chunk errored: {:?}", e);
                    thread::sleep(Duration::from_millis(args.backoff));
                    continue;
                }
            };

            // make sure the request was successful
            if resp.status() != StatusCode::OK {
                let status = resp.status();
                let body = match resp.text().await {
                    Ok(b) => b,
                    Err(e) => format!("could not get text from body: {e}"),
                };
                info!(
                    "bad return code when uploading chunk. expected 200 OK, got {}. body: {}",
                    status, body,
                );
                thread::sleep(Duration::from_millis(args.backoff));
                continue;
            }

            // the request was successful, break
            info!("uploaded chunk successfully");
            break;
        }
    }

    // tell the server we're done
    let resp = client
        .post(&done_url)
        .header(JOB_ID_HEADER, &job_id)
        .send()
        .await?;

    // make sure the request was successful
    if resp.status() != StatusCode::OK {
        info!(
            "bad return code when completing job. expected 200 OK, got {} body: {}",
            resp.status(),
            resp.text().await?
        );
    }

    // all done
    info!("stream upload complete");
    Ok(())
}
