use anyhow::{anyhow, Context};
use axum::{
    body::Bytes,
    extract::State,
    http::{HeaderMap, StatusCode},
    response::{IntoResponse, Response},
    routing::post,
    Router,
};
use clap::Parser;
use log::info;
use rpipe::consts::{EXPECTED_SIZE_HEADER, JOB_ID_HEADER};
use std::process::{Command, Stdio};
use std::{collections::HashMap, io::Write, net::SocketAddr, sync::RwLock};
use std::{process::Child, sync::Arc};
use uuid::Uuid;

struct Job {
    child: Child,
}

#[derive(Default)]
struct Server {
    jobs: HashMap<String, Job>,
}

// Make our own error that wraps `anyhow::Error`.
struct ServerError(anyhow::Error);

// Tell axum how to convert `AppError` into a response.
impl IntoResponse for ServerError {
    fn into_response(self) -> Response {
        (
            StatusCode::INTERNAL_SERVER_ERROR,
            format!("Something went wrong: {}", self.0),
        )
            .into_response()
    }
}

// This enables using `?` on functions that return `Result<_, anyhow::Error>` to turn them into
// `Result<_, AppError>`. That way you don't need to do that manually.
impl<E> From<E> for ServerError
where
    E: Into<anyhow::Error>,
{
    fn from(err: E) -> Self {
        Self(err.into())
    }
}

type SharedServer = Arc<RwLock<Server>>;

#[derive(Parser, Debug)]
#[clap(author, version, about, long_about = None)]
struct Args {
    #[clap(long, default_value_t = 3000)]
    port: u16,
}

#[tokio::main]
async fn main() {
    simple_logger::init_with_level(log::Level::Info).unwrap();
    let args = Args::parse();
    let server = SharedServer::default();

    let app = Router::new()
        .route("/create", post(create))
        .route("/upload", post(upload))
        .route("/done", post(done))
        .with_state(Arc::clone(&server));
    let addr = SocketAddr::from(([0, 0, 0, 0], args.port));
    info!("listening on {}", addr);
    axum::Server::bind(&addr)
        .serve(app.into_make_service())
        .await
        .unwrap();
}

async fn create(State(state): State<SharedServer>, command: String) -> Result<String, ServerError> {
    // the command is the text body of create

    // spawn the command into a child process
    let child = Command::new("sh")
        .arg("-c")
        .arg(command.clone())
        .stdin(Stdio::piped())
        .spawn()?;

    // create a new job id
    let job_id = Uuid::new_v4().to_string();

    // build a new job with the job id
    let job = Job { child };

    // add the job to our state
    state.write().unwrap().jobs.insert(job_id.clone(), job);

    // return the job id
    info!("created job id {} with command {}", job_id, command);
    Ok(job_id)
}

async fn upload(
    State(state): State<SharedServer>,
    headers: HeaderMap,
    bytes: Bytes,
) -> Result<String, ServerError> {
    let job_id = headers
        .get(JOB_ID_HEADER)
        .context("could not process job id header")?
        .to_str()?;

    let expected_size = headers
        .get(EXPECTED_SIZE_HEADER)
        .context("could not process size header")?
        .to_str()?
        .parse::<usize>()?;

    // make sure the length of bytes in the body is what we expected to get
    if bytes.len() != expected_size {
        return Err(ServerError(anyhow!(
            "unexpected size. expected {}, got {}",
            expected_size,
            bytes.len()
        )));
    }

    // find the job from this job_id and get a reference to stdin
    let mut locks = state.write().unwrap();
    let job = locks
        .jobs
        .get_mut(job_id)
        .context("could not find job id in memory")?;

    let stdin = job
        .child
        .stdin
        .as_mut()
        .context("could not get stdin for job")?;

    // send the bytes into the process
    stdin.write_all(&bytes)?;
    // return the job id
    info!("successfully processed chunk for job id {}", job_id);
    Ok("ok".to_string())
}

async fn done(
    State(state): State<SharedServer>,
    headers: HeaderMap,
) -> Result<String, ServerError> {
    // get the job based on the job id header
    let job_id = headers
        .get(JOB_ID_HEADER)
        .context("could not find job id header")?
        .to_str()?;

    // find the job from this job_id and remove it
    let mut locks = state.write().unwrap();
    let mut job = locks
        .jobs
        .remove(job_id)
        .context("could not find job id in memory")?;

    let stdin = job.child.stdin.take().context("could not take stdin")?;
    drop(stdin); // close stdin

    // wait for the job to end
    let status = job.child.wait().context("could not wait on child")?;
    info!("successfully complete job id {}", job_id);
    Ok(format!("{}", status))
}
