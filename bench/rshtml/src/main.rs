// Extracts the text of every file in a list with a Rust HTML crate, as
// run_html.py does with a Python tool: one process, WORKERS threads
// reading the files in turn, the time of each call after the bytes are
// read, whether it failed (an error or a panic), and the length of the
// text; the text goes to OUTDIR/text/NAME.txt, a line a file to
// OUTDIR/results.jsonl.
//
//     rshtml TOOL LIST OUTDIR WORKERS     (TOOL: html2text, htmd)
//
// Both crates take UTF-8: the bytes are read as UTF-8, invalid
// sequences as U+FFFD (String::from_utf8_lossy), as neither sniffs an
// encoding.
use std::io::Write;
use std::time::Instant;

fn main() {
    let a: Vec<String> = std::env::args().collect();
    let (tool, list, out, workers) = (a[1].clone(), a[2].clone(), a[3].clone(), a[4].parse::<usize>().unwrap());
    let files: Vec<String> = std::fs::read_to_string(&list).unwrap().lines().filter(|l| !l.is_empty()).map(String::from).collect();
    std::fs::create_dir_all(format!("{}/text", out)).unwrap();
    let mut res: Vec<String> = vec![String::new(); files.len()];
    std::thread::scope(|s| {
        let chunks: Vec<_> = (0..workers)
            .map(|k| {
                let files = &files;
                let tool = &tool;
                let out = &out;
                s.spawn(move || {
                    let mut mine = Vec::new();
                    let mut i = k;
                    while i < files.len() {
                        let b = std::fs::read(&files[i]).unwrap();
                        let name = std::path::Path::new(&files[i]).file_name().unwrap().to_string_lossy().to_string();
                        let t0 = Instant::now();
                        let r = std::panic::catch_unwind(|| {
                            let s = String::from_utf8_lossy(&b);
                            match tool.as_str() {
                                "html2text" => html2text::from_read(s.as_bytes(), 200).map_err(|e| format!("{e}")),
                                "htmd" => htmd::convert(&s).map_err(|e| format!("{e}")),
                                _ => panic!("unknown tool"),
                            }
                        });
                        let dt = t0.elapsed().as_secs_f64();
                        let (err, n) = match r {
                            Ok(Ok(text)) => {
                                std::fs::write(format!("{}/text/{}.txt", out, name), &text).unwrap();
                                (serde_json::Value::Null, text.chars().count())
                            }
                            Ok(Err(e)) => (serde_json::Value::String(e), 0),
                            Err(_) => (serde_json::Value::String("panic".into()), 0),
                        };
                        mine.push((i, serde_json::json!({"file": name, "s": dt, "err": err, "chars": n}).to_string()));
                        i += workers;
                    }
                    mine
                })
            })
            .collect();
        for c in chunks {
            for (i, line) in c.join().unwrap() {
                res[i] = line;
            }
        }
    });
    let mut w = std::fs::File::create(format!("{}/results.jsonl", out)).unwrap();
    for l in res {
        writeln!(w, "{}", l).unwrap();
    }
}
