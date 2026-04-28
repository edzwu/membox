use clap::{Parser, Subcommand};
use rand::Rng;
use std::fs;
use std::path::Path;
use std::process::Command;
use unicode_width::{UnicodeWidthChar, UnicodeWidthStr};

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
    /// List all posts
    Ls,
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

fn read_frontmatter(path: &Path) -> Option<(String, String)> {
    let content = fs::read_to_string(path).ok()?;

    let start = content.find("+++")? + 3;
    let end = content[start..].find("+++")? + start;
    let toml_str = &content[start..end];

    let value: toml::Value = toml::from_str(toml_str).ok()?;
    let title = value.get("title")?.as_str()?.to_string();
    let date = value.get("date")?.as_str()?.to_string();

    Some((title, date))
}

fn cmd_ls() {
    let notes_dir = Path::new("content/notes");
    if !notes_dir.exists() {
        eprintln!("content/notes/ 目录不存在");
        std::process::exit(1);
    }

    let mut entries: Vec<(String, String, String)> = vec![];

    for entry in fs::read_dir(notes_dir).unwrap() {
        let entry = entry.unwrap();
        let path = entry.path();

        if path.extension().and_then(|s| s.to_str()) != Some("md") {
            continue;
        }

        let id = path
            .file_stem()
            .and_then(|s| s.to_str())
            .unwrap_or("")
            .to_string();

        // 跳过 _index.md 和以 _ 开头的文件
        if id.starts_with('_') {
            continue;
        }

        // 读取文件系统修改时间
        let lastmod_str = fs::metadata(&path)
            .ok()
            .and_then(|m| m.modified().ok())
            .and_then(|t| {
                let secs = t.duration_since(std::time::UNIX_EPOCH).ok()?.as_secs() as i64;
                let dt = chrono::DateTime::from_timestamp(secs, 0)?;
                Some(dt.with_timezone(&chrono::Local).format("%Y-%m-%d").to_string())
            })
            .unwrap_or_else(|| "?".to_string());

        if let Some((title, _)) = read_frontmatter(&path) {
            entries.push((id, title, lastmod_str));
        }
    }

    // 按 lastmod 降序排列（最新的在前）
    entries.sort_by(|a, b| b.2.cmp(&a.2));

    // 输出表格
    println!("{:<12} {:<38} {}", "ID", "TITLE", "UPDATED");
    println!("{}", "-".repeat(80));
    for (id, title, lastmod) in entries {
        let id_short = &id[..id.chars().count().min(5)];

        let title_display = truncate_width(&title, 38);
        let pad = 38 - title_display.width();

        println!("{:<12} {}{:pad$} {}", id_short, title_display, "", lastmod, pad = pad);
    }

    fn truncate_width(s: &str, max_width: usize) -> String {
        let mut result = String::new();
        let mut current_width = 0;
        for ch in s.chars() {
            let w = ch.width().unwrap_or(0);
            if current_width + w > max_width - 3 {
                result.push_str("...");
                break;
            }
            result.push(ch);
            current_width += w;
        }
        result
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
        Commands::Ls => {
            cmd_ls();
        }
    }
}
