#!/usr/bin/env python3
"""Refresh the "Fleet status" section of README.md for every fork in prateek/forks.

Runs from the fleet-digest workflow. Reads per-fork state from the GitHub API
(last run, latest release, open needs-human issues) and resolver spend from the
sync commit bodies, rewrites the fenced fleet-status block in README.md, and
commits it. Best-effort: any field it can't resolve renders as an em dash.
"""

import json
import os
import re
import subprocess
from datetime import datetime, timezone
from pathlib import Path

REPO = os.environ["GITHUB_REPOSITORY"]
BOT = "github-actions[bot]"
SPEND = re.compile(r"\$([0-9]+(?:\.[0-9]+)?)")
README = Path("README.md")
START = "<!-- fleet-status:start -->"
END = "<!-- fleet-status:end -->"


def gh(*args, check=False):
    r = subprocess.run(["gh", *args], capture_output=True, text=True)
    if check and r.returncode != 0:
        raise RuntimeError(f"gh {' '.join(args)} failed: {r.stderr.strip()}")
    return r.stdout


def gh_json(*args, default):
    out = gh(*args).strip()
    if not out or out == "null":
        return default
    try:
        return json.loads(out)
    except json.JSONDecodeError:
        return default


def forks():
    return sorted(p.parent.parent.name for p in Path(".").glob("*/.fork/fork.toml"))


def last_run(tool):
    return gh_json(
        "api",
        f"repos/{REPO}/actions/workflows/{tool}.yml/runs",
        "--jq",
        ".workflow_runs[0] // empty | {conclusion, status, html_url, created_at}",
        default=None,
    )


def latest_release(tool):
    return gh_json(
        "api",
        f"repos/{REPO}/releases",
        "--jq",
        f'[.[] | select(.tag_name | startswith("{tool}-v"))][0] // empty | {{tag_name, html_url}}',
        default=None,
    )


def open_issues():
    return gh_json(
        "issue",
        "list",
        "--repo",
        REPO,
        "--state",
        "open",
        "--author",
        BOT,
        "--limit",
        "200",
        "--json",
        "number,title,url",
        default=[],
    )


def last_spend(tool):
    body = subprocess.run(
        ["git", "log", "-1", "--format=%B", "--", f"{tool}/.fork/lock.json"],
        capture_output=True,
        text=True,
    ).stdout
    hits = SPEND.findall(body)
    return f"${hits[-1]}" if hits else "—"


def row(tool, issues):
    run = last_run(tool)
    rel = latest_release(tool)
    mine = [i for i in issues if tool.lower() in i["title"].lower()]
    result = f"[{run['conclusion'] or run['status'] or '—'}]({run['html_url']})" if run else "—"
    release = f"[{rel['tag_name']}]({rel['html_url']})" if rel else "—"
    escal = " ".join(f"[#{i['number']}]({i['url']})" for i in mine) or "—"
    return f"| `{tool}` | {result} | {release} | {last_spend(tool)} | {escal} |"


def build_section():
    tools = forks()
    stamp = datetime.now(timezone.utc).strftime("%Y-%m-%d %H:%M UTC")
    lines = ["## Fleet status", ""]
    if not tools:
        lines += ["No forks in the fleet yet.", ""]
    else:
        issues = open_issues()
        lines += [
            "| fork | last run | latest release | last spend | needs-human |",
            "| --- | --- | --- | --- | --- |",
            *(row(t, issues) for t in tools),
            "",
        ]
    lines.append(f"_Updated {stamp} · run {os.environ.get('GITHUB_RUN_ID', '?')}._")
    return "\n".join(lines)


def write_readme(section):
    text = README.read_text()
    block = f"{START}\n{section}\n{END}"
    if START in text and END in text:
        text = re.sub(re.escape(START) + r".*?" + re.escape(END), block, text, count=1, flags=re.S)
    else:
        text = text.rstrip() + f"\n\n{block}\n"
    README.write_text(text)


def commit():
    subprocess.run(["git", "config", "user.name", "fleet-digest"], check=True)
    subprocess.run(["git", "config", "user.email", "fleet-digest@invalid"], check=True)
    subprocess.run(["git", "add", "README.md"], check=True)
    if subprocess.run(["git", "diff", "--cached", "--quiet"]).returncode == 0:
        print("fleet status unchanged")
        return
    subprocess.run(["git", "commit", "-q", "-m", "chore(fleet): update status"], check=True)
    ref = os.environ.get("GITHUB_REF_NAME", "main")
    subprocess.run(["git", "push", "--quiet", "origin", f"HEAD:{ref}"], check=True)
    print("fleet status updated")


if __name__ == "__main__":
    write_readme(build_section())
    commit()
