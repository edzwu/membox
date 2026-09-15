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
    Ls {
        /// Show English posts
        #[arg(long, conflicts_with = "zh")]
        en: bool,
        /// Show Chinese posts
        #[arg(long, conflicts_with = "en")]
        zh: bool,
    },
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
        let file_path = format!("{}/{}.md", notes_dir, id);
        let bundle_path = format!("{}/{}", notes_dir, id);
        if fs::metadata(&file_path).is_err() && fs::metadata(&bundle_path).is_err() {
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

fn note_id_from_path(path: &Path) -> Option<String> {
    let file_name = path.file_name()?.to_str()?;
    if file_name == "index.md" || (file_name.starts_with("index.") && file_name.ends_with(".md")) {
        return path.parent()?.file_name()?.to_str().map(|s| s.to_string());
    }
    let stem = path.file_stem()?.to_str()?;
    Some(stem.to_string())
}

fn note_content_path(path: &Path, lang: Option<&str>) -> Option<std::path::PathBuf> {
    if path.is_file() {
        if path.extension().and_then(|s| s.to_str()) == Some("md") {
            let id = path.file_stem()?.to_str()?;
            if !id.starts_with('_') {
                return Some(path.to_path_buf());
            }
        }
        return None;
    }

    if !path.is_dir() {
        return None;
    }

    let preferred = match lang {
        Some("en") => vec!["index.en.md"],
        Some("zh") => vec!["index.zh.md"],
        _ => vec!["index.en.md", "index.zh.md", "index.md"],
    };
    for name in preferred {
        let candidate = path.join(name);
        if candidate.is_file() {
            return Some(candidate);
        }
    }

    if lang.is_some() {
        return None;
    }

    let mut candidates = fs::read_dir(path)
        .ok()?
        .filter_map(|entry| entry.ok().map(|entry| entry.path()))
        .filter(|path| {
            path.is_file()
                && path.extension().and_then(|s| s.to_str()) == Some("md")
                && path
                    .file_name()
                    .and_then(|s| s.to_str())
                    .is_some_and(|name| name.starts_with("index."))
        })
        .collect::<Vec<_>>();
    candidates.sort();
    candidates.into_iter().next()
}

fn cmd_ls(lang: Option<&str>) {
    let notes_dir = Path::new("content/notes");
    if !notes_dir.exists() {
        eprintln!("content/notes/ 目录不存在");
        std::process::exit(1);
    }

    let mut entries: Vec<(String, String, String)> = vec![];

    for entry in fs::read_dir(notes_dir).unwrap() {
        let entry = entry.unwrap();
        let Some(path) = note_content_path(&entry.path(), lang) else {
            continue;
        };
        let Some(id) = note_id_from_path(&path) else {
            continue;
        };
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
                Some(
                    dt.with_timezone(&chrono::Local)
                        .format("%Y-%m-%d")
                        .to_string(),
                )
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

        println!(
            "{:<12} {}{:pad$} {}",
            id_short,
            title_display,
            "",
            lastmod,
            pad = pad
        );
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
            let path = format!("content/notes/{}/index.en.md", id);
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
        Commands::Ls { en, zh } => {
            let lang = if en {
                Some("en")
            } else if zh {
                Some("zh")
            } else {
                None
            };
            cmd_ls(lang);
        }
    }
}
