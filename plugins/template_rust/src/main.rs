use std::collections::HashMap;
use std::env;
use std::io::{self, Write};
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
                    out.push(if t { c.to_ascii_uppercase() } else { c.to_ascii_lowercase() });
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
        if let Err(e) = run_zmq(&name, &endpoint) {
            log(&format!("zmq error: {}", e));
        }
        return;
    }

    log("idle mode");
    loop {
        thread::sleep(Duration::from_secs(3600));
    }
}

fn run_zmq(name: &str, endpoint: &str) -> Result<(), String> {
    let ctx = zmq::Context::new();
    let sock = ctx.socket(zmq::DEALER).map_err(|e| e.to_string())?;
    sock.set_identity(name.as_bytes()).map_err(|e| e.to_string())?;
    sock.connect(endpoint).map_err(|e| e.to_string())?;
    sock.send(
        format!("{{\"type\":\"hello\",\"name\":{}}}", json_str(name)).as_bytes(),
        0,
    )
    .map_err(|e| e.to_string())?;
    log("connected");

    let mut map: HashMap<String, Vec<String>> = HashMap::new();

    loop {
        let msg = sock.recv_msg(0).map_err(|e| e.to_string())?;
        let data = String::from_utf8_lossy(&msg);
        let (typ, qid, text) = parse_json(&data);

        match typ.as_deref() {
            Some("request") => {
                let qid = qid.unwrap_or_default();
                let text = text.unwrap_or_default();
                let v = process(&text);
                map.insert(qid.clone(), v.clone());
                let resp = format!(
                    "{{\"type\":\"response\",\"query_id\":{},\"data\":{{\"input\":{},\"variants\":[{{\"id\":\"1\",\"label\":{}}},{{\"id\":\"2\",\"label\":{}}},{{\"id\":\"3\",\"label\":{}}}]}}}}",
                    json_str(&qid), json_str(&text), json_str(&v[0]), json_str(&v[1]), json_str(&v[2])
                );
                sock.send(resp.as_bytes(), 0).map_err(|e| e.to_string())?;
                log(&format!("response sent qid={}", qid));
            }
            Some("select") => {
                let qid = qid.unwrap_or_default();
                let token = text.unwrap_or_default();
                let val = token
                    .parse::<usize>()
                    .ok()
                    .and_then(|i| map.get(&qid)?.get(i.wrapping_sub(1)))
                    .cloned()
                    .unwrap_or_default();
                log(&format!("selection qid={} token={} val={}", qid, token, val));
            }
            _ => {}
        }
    }
}

fn parse_json(s: &str) -> (Option<String>, Option<String>, Option<String>) {
    let (mut typ, mut qid, mut text) = (None, None, None);
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
            _ => {}
        }
    }
    (typ, qid, text)
}
