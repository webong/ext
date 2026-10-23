//! The language-neutral fixtures for progress events and reverse calls
//! (lib/plugin/testdata/v1), which the Go contract computes and every SDK must
//! reproduce: frames, the sender's rate limiter and the wire model.
use ext_plugin::{
    frames::{check_progress_frame, check_reverse_request, check_reverse_response, Limiter},
    wire, Descriptor, ProgressLimits, Request,
};
use serde_json::Value;

fn fixture(text: &str) -> Value {
    serde_json::from_str(text).unwrap()
}
fn cases<'a>(root: &'a Value, key: &str) -> &'a [Value] {
    root[key].as_array().unwrap_or_else(|| panic!("no {key}"))
}
fn text<'a>(case: &'a Value, key: &str) -> &'a str {
    case[key].as_str().unwrap()
}
// Collects the first few mismatches so a failure says what diverged.
#[derive(Default)]
struct Mismatches(Vec<String>, usize);
impl Mismatches {
    fn note(&mut self, section: &str, case: &Value, what: String) {
        self.1 += 1;
        if self.0.len() < 12 {
            self.0
                .push(format!("{section}: {}: {what}", text(case, "name")));
        }
    }
    fn finish(self) {
        assert!(
            self.1 == 0,
            "{} mismatches, the first {}:\n{}",
            self.1,
            self.0.len(),
            self.0.join("\n")
        );
    }
}

#[test]
fn frames_are_judged_as_the_go_contract_judges_them() {
    let root = fixture(include_str!(
        "../../../lib/plugin/testdata/v1/frame-cases.json"
    ));
    let mut bad = Mismatches::default();
    let mut total = 0;
    for (section, judge) in [
        (
            "progress",
            (|line: &[u8], _: &str| check_progress_frame(line).is_ok()) as fn(&[u8], &str) -> bool,
        ),
        ("reverseRequest", |line, _| {
            check_reverse_request(line).is_ok()
        }),
        ("reverseResponse", |line, id| {
            check_reverse_response(line, id).is_ok()
        }),
    ] {
        let list = cases(&root, section);
        assert!(list.len() >= 20, "{section} has {} cases", list.len());
        let valid = list.iter().filter(|c| c["valid"] == true).count();
        assert!(
            valid >= 5 && list.len() - valid >= 5,
            "{section} proves little"
        );
        for case in list {
            total += 1;
            let id = case["id"].as_str().unwrap_or("");
            let got = judge(text(case, "frame").as_bytes(), id);
            let want = case["valid"].as_bool().unwrap();
            if got != want {
                bad.note(
                    section,
                    case,
                    format!("accepted {got}, want {want}: {}", text(case, "frame")),
                );
            }
        }
    }
    assert!(total >= 500);
    bad.finish();
}

#[test]
fn the_rate_limiter_reproduces_every_step() {
    let root = fixture(include_str!(
        "../../../lib/plugin/testdata/v1/progress-limiter-cases.json"
    ));
    let mut bad = Mismatches::default();
    let list = cases(&root, "cases");
    assert!(list.len() >= 10);
    for case in list {
        let mut limiter = Limiter::new(
            ProgressLimits {
                events_per_second: case["eventsPerSecond"].as_i64().unwrap(),
                bytes_per_second: case["bytesPerSecond"].as_i64().unwrap(),
            },
            0,
        );
        for (i, step) in case["steps"].as_array().unwrap().iter().enumerate() {
            let got = limiter.admit(
                step["textBytes"].as_u64().unwrap() as usize,
                step["atMs"].as_i64().unwrap() * 1_000_000,
            );
            let want = step["admitted"].as_bool().unwrap();
            let want_dropped = step["dropped"].as_u64().unwrap_or(0);
            if got.is_some() != want || (want && got != Some(want_dropped)) {
                bad.note(
                    "limiter",
                    case,
                    format!("step {i}: {got:?}, want {want} {want_dropped}"),
                );
            }
        }
        if limiter.pending() != case["pending"].as_u64().unwrap() {
            bad.note("limiter", case, "pending count".into());
        }
    }
    bad.finish();
}

fn descriptor_of(raw: &str) -> Option<Descriptor> {
    let d: Descriptor = wire::typed(wire::decode(raw.as_bytes()).ok()?).ok()?;
    d.validate().ok()?;
    Some(d)
}

#[test]
fn the_wire_model_is_judged_as_the_go_contract_judges_it() {
    let root = fixture(include_str!(
        "../../../lib/plugin/testdata/v1/model-cases.json"
    ));
    let mut bad = Mismatches::default();
    let descriptors = cases(&root, "descriptors");
    assert!(descriptors.len() > 100);
    for case in descriptors {
        let got = descriptor_of(text(case, "descriptor")).is_some();
        let want = case["valid"].as_bool().unwrap();
        if got != want {
            bad.note(
                "descriptor",
                case,
                format!("accepted {got}, want {want}: {}", text(case, "descriptor")),
            );
        }
    }
    let selected: Descriptor = serde_json::from_value(root["requestDescriptor"].clone()).unwrap();
    selected.validate().unwrap();
    let requests = cases(&root, "requests");
    assert!(requests.len() > 100);
    for case in requests {
        let parsed: Option<Request> = wire::decode(text(case, "request").as_bytes())
            .ok()
            .and_then(|v| wire::typed(v).ok())
            .filter(|r: &Request| r.validate(&selected).is_ok());
        let want = case["valid"].as_bool().unwrap();
        if parsed.is_some() != want {
            bad.note(
                "request",
                case,
                format!(
                    "accepted {}, want {want}: {}",
                    parsed.is_some(),
                    text(case, "request")
                ),
            );
            continue;
        }
        let Some(r) = parsed else { continue };
        // The call gets a reporter when its operation declared progress and the
        // request carries a rate; the rate has its defaults filled in.
        let declared = selected
            .lookup(&r.contract, &r.operation)
            .map(|op| op.progress)
            .unwrap_or(false);
        let reporter = declared && r.progress.is_some();
        if reporter != case["reporter"].as_bool().unwrap_or(false) {
            bad.note("request", case, format!("reporter {reporter}"));
        } else if reporter {
            let l = r.progress.unwrap().resolved();
            if l.events_per_second != case["events"].as_i64().unwrap()
                || l.bytes_per_second != case["bytes"].as_i64().unwrap()
            {
                bad.note("request", case, format!("rate {l:?}"));
            }
        }
    }
    bad.finish();
}
