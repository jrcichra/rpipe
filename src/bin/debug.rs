use clap::Parser;
use log::info;
use std::{
    fs::File,
    io::{Read, Seek, SeekFrom},
};

#[derive(Parser, Debug)]
#[clap(author, version, about, long_about = None)]
struct Args {
    #[clap(short, long)]
    source: String,
    #[clap(short, long)]
    dest: String,
    #[clap( long,default_value_t= 1 * 1024 * 1024)]
    chunk_size: usize,
}

#[tokio::main]
async fn main() -> Result<(), anyhow::Error> {
    simple_logger::init_with_level(log::Level::Info)?;
    let args = Args::parse();

    // open each file
    let mut source = File::open(args.source)?;
    let mut dest = File::open(args.dest)?;

    // we know where we need to seek to to start this so might as well
    let offset = 3514826753;
    source.seek(SeekFrom::Start(offset - (1 * 1024 * 1024)))?;
    dest.seek(SeekFrom::Start(offset))?; // jump back a meg on the destination and see if things still line up

    // set their positions to 0
    let mut source_position: usize = 0;
    // let mut dest_position: usize = 0;

    loop {
        // build buffers for each file
        let mut source_buf = vec![0; args.chunk_size];
        let mut dest_buf = vec![0; args.chunk_size];

        // read chunk_size from each file
        source.read(&mut source_buf)?;
        dest.read(&mut dest_buf)?;

        // compare the buffer bytes
        for i in 0..source_buf.len() {
            // dest_position += 1;
            source_position += 1;
            if source_buf[i] != dest_buf[i] {
                info!(
                "difference between source_buf and dest_buf at position {}: source: {}, dest: {}",
                source_position, source_buf[i], dest_buf[i]
            );
            }
        }
    }
}
