use std::collections::HashMap;
use std::env;
use std::io::{self, BufRead, BufReader, Write};
use std::os::unix::net::UnixStream;
use std::process::Command;
use std::thread;
use std::time::{Duration, SystemTime, UNIX_EPOCH};

fn log(msg: &str) {
    let name = env::var("TARRAGON_PLUGIN_NAME").unwrap_or("template_rust".into());
    let _ = writeln!(io::stderr(), "[PLUGIN: {}] {}", name, msg);
}

fn transforms() -> Vec<fn(&str) -> String> {
    vec![
        |s| s.chars().rev().collect(),
        |s| s.to_uppercase(),
        |s| {
            s.split_whitespace()
                .map(|w| {
                    let mut c = w.chars();
                    match c.next() {
                        Some(h) => h.to_uppercase().collect::<String>() + c.as_str(),
                        None => String::new(),
                    }
                })
                .collect::<Vec<_>>()
                .join(" ")
        },
        |s| {
            s.chars()
                .map(|c| match c {
                    'A'..='Z' => (((c as u8 - b'A' + 13) % 26) + b'A') as char,
                    'a'..='z' => (((c as u8 - b'a' + 13) % 26) + b'a') as char,
                    _ => c,
                })
                .collect()
        },
        |s| {
            let mut v: Vec<char> = s.chars().collect();
            v.sort_unstable();
            v.into_iter().collect()
        },
        shuffle_letters,
        |s| {
            let (mut out, mut t) = (String::new(), false);
            for c in s.chars() {
                if c.is_alphabetic() {
                    out.push(if t {
                        c.to_ascii_uppercase()
                    } else {
                        c.to_ascii_lowercase()
                    });
                    t = !t;
                } else {
                    out.push(c);
                }
            }
            out
        },
        |s| s.chars().filter(|c| !"aeiouAEIOU".contains(*c)).collect(),
    ]
}

fn shuffle_letters(s: &str) -> String {
    let mut chars: Vec<char> = s.chars().collect();
    let mut rng = XorShift64::new(now_nanos());
    for i in (1..chars.len()).rev() {
        chars.swap(i, rng.next_usize(i + 1));
    }
    chars.into_iter().collect()
}

struct XorShift64(u64);
impl XorShift64 {
    fn new(seed: u64) -> Self {
        Self(seed | 1)
    }
    fn next(&mut self) -> u64 {
        self.0 ^= self.0 << 13;
        self.0 ^= self.0 >> 7;
        self.0 ^= self.0 << 17;
        self.0
    }
    fn next_usize(&mut self, bound: usize) -> usize {
        (self.next() as usize) % bound
    }
}

fn now_nanos() -> u64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .unwrap_or_default()
        .as_nanos() as u64
}

fn process(text: &str) -> Vec<String> {
    let funcs = transforms();
    let mut rng = XorShift64::new(now_nanos());
    let mut chosen = Vec::new();
    while chosen.len() < 3 {
        let idx = rng.next_usize(funcs.len());
        if !chosen.contains(&idx) {
            chosen.push(idx);
        }
    }
    chosen.into_iter().map(|i| funcs[i](text)).collect()
}

fn json_str(s: &str) -> String {
    format!(
        "\"{}\"",
        s.replace('\\', "\\\\")
            .replace('"', "\\\"")
            .replace('\n', "\\n")
            .replace('\r', "\\r")
            .replace('\t', "\\t")
    )
}

fn copy_to_clipboard(text: &str) -> (bool, String) {
    // Try wl-copy first (Wayland)
    if let Ok(mut child) = Command::new("wl-copy").arg("--").arg(text).spawn() {
        match child.wait() {
            Ok(status) if status.success() => return (true, "Copied to clipboard".into()),
            _ => {}
        }
    }
    // Fallback to xclip (X11)
    if let Ok(mut child) = Command::new("xclip")
        .args(["-selection", "clipboard"])
        .stdin(std::process::Stdio::piped())
        .spawn()
    {
        if let Some(mut stdin) = child.stdin.take() {
            use std::io::Write;
            let _ = stdin.write_all(text.as_bytes());
        }
        match child.wait() {
            Ok(status) if status.success() => return (true, "Copied to clipboard".into()),
            _ => {}
        }
    }
    (false, "No clipboard tool found".into())
}

fn main() {
    log("initializing");
    let args: Vec<_> = env::args().skip(1).collect();

    if args.len() >= 2 && args[0] == "--once" {
        let text = args[1..].join(" ");
        let v = process(&text);
        println!(
            "{{\"input\":{},\"variants\":[{},{},{}]}}",
            json_str(&text),
            json_str(&v[0]),
            json_str(&v[1]),
            json_str(&v[2])
        );
        return;
    }

    if let Ok(endpoint) = env::var("TARRAGON_PLUGINS_ENDPOINT") {
        let name = env::var("TARRAGON_PLUGIN_NAME").unwrap_or("template_rust".into());
        log(&format!("connecting to {}", endpoint));
        if let Err(e) = run_uds(&name, &endpoint) {
            log(&format!("uds error: {}", e));
        }
        return;
    }

    log("idle mode");
    loop {
        thread::sleep(Duration::from_secs(3600));
    }
}

fn run_uds(name: &str, endpoint: &str) -> Result<(), String> {
    // Retry connection in case the daemon listener isn't ready yet.
    let stream = {
        let mut last_err = String::new();
        let mut conn = None;
        for _ in 0..20 {
            match UnixStream::connect(endpoint) {
                Ok(s) => {
                    conn = Some(s);
                    break;
                }
                Err(e) => {
                    last_err = e.to_string();
                    thread::sleep(Duration::from_millis(100));
                }
            }
        }
        conn.ok_or(last_err)?
    };
    let mut writer = stream.try_clone().map_err(|e| e.to_string())?;
    let reader = BufReader::new(stream);
    writeln!(
        writer,
        "{}",
        format!("{{\"type\":\"hello\",\"name\":{}}}", json_str(name))
    )
    .map_err(|e| e.to_string())?;
    log("connected");

    let mut map: HashMap<String, Vec<String>> = HashMap::new();

    for line in reader.lines() {
        let line = line.map_err(|e| e.to_string())?;
        let (typ, qid, text, result_id, action) = parse_json(&line);

        match typ.as_deref() {
            Some("request") => {
                let qid = qid.unwrap_or_default();
                let text = text.unwrap_or_default();
                let v = process(&text);
                map.insert(qid.clone(), v.clone());
                let resp = format!(
                    "{{\"type\":\"response\",\"query_id\":{},\"data\":{{\"input\":{},\"variants\":[{{\"id\":\"1\",\"label\":{},\"actions\":[{{\"name\":\"copy\",\"default\":true,\"description\":\"Copy to clipboard\"}}]}},{{\"id\":\"2\",\"label\":{},\"actions\":[{{\"name\":\"copy\",\"default\":true,\"description\":\"Copy to clipboard\"}}]}},{{\"id\":\"3\",\"label\":{},\"actions\":[{{\"name\":\"copy\",\"default\":true,\"description\":\"Copy to clipboard\"}}]}}]}}}}",
                    json_str(&qid), json_str(&text), json_str(&v[0]), json_str(&v[1]), json_str(&v[2])
                );
                writeln!(writer, "{}", resp).map_err(|e| e.to_string())?;
                log(&format!("response sent qid={}", qid));
            }
            Some("select") => {
                let qid = qid.unwrap_or_default();
                let result_id = result_id.unwrap_or_default();
                let _action = action.unwrap_or_default();
                let (success, message) = match result_id
                    .parse::<usize>()
                    .ok()
                    .and_then(|i| map.get(&qid)?.get(i.wrapping_sub(1)))
                    .cloned()
                {
                    Some(val) => copy_to_clipboard(&val),
                    None => (false, format!("Unknown result: {}", result_id)),
                };
                let resp = format!(
                    "{{\"type\":\"select_response\",\"success\":{},\"message\":{}}}",
                    success,
                    json_str(&message)
                );
                writeln!(writer, "{}", resp).map_err(|e| e.to_string())?;
                log(&format!("select_response qid={} success={}", qid, success));
            }
            _ => {}
        }
    }

    Ok(())
}

fn parse_json(
    s: &str,
) -> (
    Option<String>,
    Option<String>,
    Option<String>,
    Option<String>,
    Option<String>,
) {
    let (mut typ, mut qid, mut text, mut result_id, mut action) = (None, None, None, None, None);
    for part in s.trim_matches(|c| c == '{' || c == '}').split(',') {
        let mut kv = part.splitn(2, ':');
        let (k, v) = match (kv.next(), kv.next()) {
            (Some(k), Some(v)) => (k.trim().trim_matches('"'), v.trim().trim_matches('"')),
            _ => continue,
        };
        match k {
            "type" => typ = Some(v.into()),
            "query_id" => qid = Some(v.into()),
            "text" => text = Some(v.into()),
            "result_id" => result_id = Some(v.into()),
            "action" => action = Some(v.into()),
            _ => {}
        }
    }
    (typ, qid, text, result_id, action)
}
