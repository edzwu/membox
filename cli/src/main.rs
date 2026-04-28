use clap::{Parser, Subcommand};
use rand::Rng;
use std::fs;
use std::process::Command;

#[derive(Parser)]
#[command(name = "ag")]
#[command(about = "Agora blog CLI")]
struct Cli {
    #[command(subcommand)]
    command: Commands,
}

#[derive(Subcommand)]
enum Commands {
    /// Create a new post with random ID
    New,
    /// Build the site (hugo --minify)
    Build,
    /// Serve locally (hugo server)
    Serve,
}

fn generate_id() -> String {
    let timestamp = chrono::Utc::now().timestamp_millis();
    let time_part = format!("{:x}", timestamp);
    let time_part = &time_part[time_part.len().saturating_sub(8)..];

    let mut rng = rand::thread_rng();
    let rand_part: String = (0..4)
        .map(|_| format!("{:x}", rng.gen_range(0..16)))
        .collect();

    format!("{}{}", time_part, rand_part)
}

fn generate_unique_id() -> String {
    let notes_dir = "content/notes";
    let mut attempts = 0;
    loop {
        let id = generate_id();
        let path = format!("{}/{}.md", notes_dir, id);
        if fs::metadata(&path).is_err() {
            return id;
        }
        attempts += 1;
        if attempts > 1000 {
            eprintln!("无法生成唯一 ID");
            std::process::exit(1);
        }
    }
}

fn main() {
    let cli = Cli::parse();

    match cli.command {
        Commands::New => {
            let id = generate_unique_id();
            let path = format!("content/notes/{}.md", id);
            let status = Command::new("hugo")
                .args(&["new", &path])
                .status()
                .expect("Failed to run hugo new");

            if status.success() {
                println!("  ID: {}", id);
            }
        }
        Commands::Build => {
            let status = Command::new("hugo")
                .arg("--minify")
                .status()
                .expect("Failed to build");

            if !status.success() {
                std::process::exit(1);
            }
        }
        Commands::Serve => {
            let status = Command::new("hugo")
                .args(&["server", "--disableFastRender"])
                .status()
                .expect("Failed to serve");

            if !status.success() {
                std::process::exit(1);
            }
        }
    }
}
