use std::env;
use std::fs::{self, OpenOptions};
use std::io::{self, Write};
use std::path::PathBuf;
use std::thread;
use std::time::{Duration, SystemTime, UNIX_EPOCH};

fn log_path() -> PathBuf {
    let cache = env::var("XDG_CACHE_HOME").ok().map(PathBuf::from).unwrap_or_else(|| {
        let mut p = dirs_home();
        p.push(".cache");
        p
    });
    let mut p = cache;
    p.push("tarragon");
    p.push("plugins");
    p.push("template_rust");
    let _ = fs::create_dir_all(&p);
    p.push("plugin.log");
    p
}

fn dirs_home() -> PathBuf {
    env::var("HOME").map(PathBuf::from).unwrap_or_else(|_| PathBuf::from("."))
}

fn log_line(msg: &str) {
    let line = format!("{}\n", msg);
    // stderr
    let _ = io::stderr().write_all(line.as_bytes());
    // file
    if let Ok(mut f) = OpenOptions::new().create(true).append(true).open(log_path()) {
        let _ = f.write_all(line.as_bytes());
    }
}

fn transforms() -> Vec<fn(&str) -> String> {
    vec![
        |s| s.chars().rev().collect::<String>(), // reverse
        |s| s.to_uppercase(),                    // upper
        |s| s.split_whitespace().map(|w| {
            let mut c = w.chars();
            match c.next() { Some(h) => h.to_uppercase().collect::<String>() + c.as_str(), None => String::new() }
        }).collect::<Vec<_>>().join(" "),
        rot13,
        |s| { let mut v: Vec<char> = s.chars().collect(); v.sort_unstable(); v.into_iter().collect::<String>() },
        shuffle_letters,
        alternating_caps,
        |s| s.chars().filter(|c| !"aeiouAEIOU".contains(*c)).collect::<String>(),
    ]
}

fn rot13(s: &str) -> String {
    s.chars().map(|c| match c {
        'A'..='Z' => (((c as u8 - b'A' + 13) % 26) + b'A') as char,
        'a'..='z' => (((c as u8 - b'a' + 13) % 26) + b'a') as char,
        _ => c,
    }).collect()
}

// Simple xorshift PRNG (no std rng to avoid deps)
#[derive(Clone)]
struct XorShift64(u64);
impl XorShift64 {
    fn new(seed: u64) -> Self { Self(seed | 1) }
    fn next(&mut self) -> u64 {
        let mut x = self.0;
        x ^= x << 13;
        x ^= x >> 7;
        x ^= x << 17;
        self.0 = x;
        x
    }
    fn next_usize(&mut self, bound: usize) -> usize { (self.next() as usize) % bound }
}

fn shuffle_letters(s: &str) -> String {
    let mut chars: Vec<char> = s.chars().collect();
    let mut rng = XorShift64::new(now_nanos());
    let len = chars.len();
    for i in (1..len).rev() {
        let j = rng.next_usize(i + 1);
        chars.swap(i, j);
    }
    chars.into_iter().collect()
}

fn alternating_caps(s: &str) -> String {
    let mut out = String::with_capacity(s.len());
    let mut toggle = false;
    for ch in s.chars() {
        if ch.is_alphabetic() {
            if toggle { out.push(ch.to_ascii_uppercase()); } else { out.push(ch.to_ascii_lowercase()); }
            toggle = !toggle;
        } else { out.push(ch); }
    }
    out
}

fn now_nanos() -> u64 {
    SystemTime::now().duration_since(UNIX_EPOCH).unwrap_or(Duration::from_secs(0)).as_nanos() as u64
}

fn process(text: &str) -> Vec<String> {
    let funcs = transforms();
    let mut rng = XorShift64::new(now_nanos());
    let mut chosen = Vec::new();
    while chosen.len() < 3 {
        let idx = rng.next_usize(funcs.len());
        if !chosen.contains(&idx) { chosen.push(idx); }
    }
    chosen.into_iter().map(|i| funcs[i](text)).collect()
}

fn main() {
    log_line("[template_rust] initializing plugin");
    let mut args = env::args().skip(1).collect::<Vec<_>>();
    if args.len() >= 2 && args[0] == "--once" {
        let text = args.split_off(1).join(" ");
        log_line(&format!("[template_rust] request received: {}", text));
        let variants = process(&text);
        log_line(&format!("[template_rust] response generated: {:?}", variants));
        // Print JSON
        let json = format!(
            "{{\"input\":{},\"variants\":[{}, {}, {}]}}",
            json_string(&text), json_string(&variants[0]), json_string(&variants[1]), json_string(&variants[2])
        );
        println!("{}", json);
        return;
    }

    if let Ok(endpoint) = env::var("TARRAGON_PLUGINS_ENDPOINT") {
        let name = env::var("TARRAGON_PLUGIN_NAME").unwrap_or_else(|_| "template_rust".to_string());
        log_line(&format!("[template_rust] connecting to ROUTER endpoint: {} as {}", endpoint, name));
        if let Err(e) = run_zmq(&name, &endpoint) {
            log_line(&format!("[template_rust] zmq error: {}", e));
        }
        return;
    }

    log_line("[template_rust] plugin started successfully; awaiting work (idle mode)");
    loop {
        thread::sleep(Duration::from_secs(5));
        log_line("[template_rust] heartbeat: running");
    }
}

fn run_zmq(name: &str, endpoint: &str) -> Result<(), String> {
    let ctx = zmq::Context::new();
    let sock = ctx.socket(zmq::DEALER).map_err(|e| e.to_string())?;
    sock.set_identity(name.as_bytes()).map_err(|e| e.to_string())?;
    sock.connect(endpoint).map_err(|e| e.to_string())?;
    let hello = format!("{{\"type\":\"hello\",\"name\":{}}}", json_string(name));
    sock.send(hello.as_bytes(), 0).map_err(|e| e.to_string())?;
    log_line("[template_rust] hello sent; entering recv loop");

    loop {
        let msg = sock.recv_msg(0).map_err(|e| e.to_string())?;
        let data = String::from_utf8(msg.to_vec()).map_err(|e| e.to_string())?;
        let parsed = parse_json(&data);
        if parsed.0.as_deref() != Some("request") {
            continue;
        }
        let qid = parsed.1.unwrap_or_else(|| "".to_string());
        let text = parsed.2.unwrap_or_else(|| "".to_string());
        log_line(&format!("[template_rust] request received qid={} text={}", qid, text));
        let body = match std::panic::catch_unwind(|| process(&text)) {
            Ok(v) => format!(
                "{{\"input\":{},\"variants\":[{}, {}, {}]}}",
                json_string(&text), json_string(&v[0]), json_string(&v[1]), json_string(&v[2])
            ),
            Err(_) => format!("{{\"error\":{}}}", json_string("processing failed")),
        };
        let resp = format!(
            "{{\"type\":\"response\",\"query_id\":{},\"data\":{}}}",
            json_string(&qid), body
        );
        sock.send(resp.as_bytes(), 0).map_err(|e| e.to_string())?;
        log_line(&format!("[template_rust] response sent qid={}", qid));
    }
}

// very small JSON parser for our specific fields
fn parse_json(s: &str) -> (Option<String>, Option<String>, Option<String>) {
    let mut typ = None; let mut qid = None; let mut text = None;
    for part in s.trim_matches(|c| c == '{' || c == '}').split(',') {
        let kv: Vec<&str> = part.splitn(2, ':').collect();
        if kv.len() != 2 { continue; }
        let k = kv[0].trim().trim_matches('"');
        let v = kv[1].trim().trim_matches('"');
        match k {
            "type" => typ = Some(v.to_string()),
            "query_id" => qid = Some(v.to_string()),
            "text" => text = Some(v.to_string()),
            _ => {}
        }
    }
    (typ, qid, text)
}

fn json_string(s: &str) -> String {
    let mut out = String::with_capacity(s.len() + 2);
    out.push('"');
    for ch in s.chars() {
        match ch {
            '\\' => out.push_str("\\\\"),
            '"' => out.push_str("\\\""),
            '\n' => out.push_str("\\n"),
            '\r' => out.push_str("\\r"),
            '\t' => out.push_str("\\t"),
            _ => out.push(ch),
        }
    }
    out.push('"');
    out
}
