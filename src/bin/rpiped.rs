use anyhow::{anyhow, Context};
use axum::{
    body::Bytes,
    extract::State,
    http::{HeaderMap, StatusCode},
    response::{IntoResponse, Response},
    routing::post,
    Json, Router,
};
use clap::Parser;
use log::info;
use rpipe::consts::{EXPECTED_POSITION_HEADER, EXPECTED_SIZE_HEADER, JOB_ID_HEADER};
use std::{collections::HashMap, io::Write, sync::RwLock};
use std::{
    io::BufWriter,
    process::{Command, Stdio},
};
use std::{process::Child, sync::Arc};
use tokio::net::TcpListener;
use uuid::Uuid;

struct Job {
    child: Child,
    position: usize,
}

#[derive(Default)]
struct Server {
    jobs: HashMap<String, Job>,
}

// Make our own error that wraps `anyhow::Error`.
struct ServerError {
    error: anyhow::Error,
    details: Option<HashMap<String, String>>,
}

// Tell axum how to convert `AppError` into a response.
impl IntoResponse for ServerError {
    fn into_response(self) -> Response {
        // add details if there are any
        let mut json_data = HashMap::new();
        if let Some(details) = self.details {
            json_data = details;
        }
        json_data.insert("error".to_string(), self.error.to_string());

        // Make a response but then just extract the body so we can wrap what we want around it
        // TODO: make this less of a hack
        let response = Json(json_data).into_response();
        Response::builder()
            .status(StatusCode::INTERNAL_SERVER_ERROR.as_u16())
            .body(response.into_body())
            .unwrap()
    }
}
// This enables using `?` on functions that return `Result<_, anyhow::Error>` to turn them into
// `Result<_, AppError>`. That way you don't need to do that manually.
impl<E> From<E> for ServerError
where
    E: Into<anyhow::Error>,
{
    fn from(err: E) -> Self {
        Self {
            error: err.into(),
            details: None, // no details on a plain anyhow
        }
    }
}

type SharedServer = Arc<RwLock<Server>>;

#[derive(Parser, Debug)]
#[clap(author, version, about, long_about = None)]
struct Args {
    #[clap(long, env, default_value_t = 3000)]
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

    let bind = format!("0.0.0.0:{}", args.port);
    info!("listening on {}", &bind);
    let listener = TcpListener::bind(&bind).await.unwrap();
    axum::serve(listener, app).await.unwrap();
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
    let job = Job { child, position: 0 };

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

    let client_expected_position = headers
        .get(EXPECTED_POSITION_HEADER)
        .context("could not process position header")?
        .to_str()?
        .parse::<usize>()?;

    // make sure the length of bytes in the body is what we expected to get
    let received_size = bytes.len();
    if received_size != expected_size {
        let mut details = HashMap::new();
        details.insert("expected".to_string(), expected_size.to_string());
        details.insert("received".to_string(), received_size.to_string());
        return Err(ServerError {
            details: Some(details),
            error: anyhow!(
                "unexpected size. expected {}, received {}",
                expected_size,
                received_size
            ),
        });
    }

    // find the job from this job_id and get a reference to stdin
    let mut locks = state.write().unwrap();
    let job = locks
        .jobs
        .get_mut(job_id)
        .context("could not find job id in memory")?;

    // make sure the position matches what we want
    let our_expected_position = job.position + expected_size;
    if client_expected_position != our_expected_position {
        let mut details = HashMap::new();
        details.insert("expected".to_string(), our_expected_position.to_string());
        details.insert("received".to_string(), client_expected_position.to_string());
        return Err(ServerError {
            details: Some(details),
            error: anyhow!(
                "unexpected position. expected {}, received {}",
                our_expected_position,
                client_expected_position
            ),
        });
    }

    let stdin = job
        .child
        .stdin
        .as_mut()
        .context("could not get stdin for job")?;

    // send the bytes into the process

    let mut writer = BufWriter::new(stdin);
    writer.write_all(&bytes)?;
    writer.flush()?;

    // update the job with the new position
    job.position = our_expected_position;

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
    info!("successfully completed job id {}", job_id);
    Ok(format!("{}", status))
}
