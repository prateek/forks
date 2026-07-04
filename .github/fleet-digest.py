#!/usr/bin/env python3
"""Upsert a pinned "fleet status" issue summarizing every fork in prateek/forks.

Runs from the fleet-digest workflow. Reads per-fork state from the GitHub API
(last run, latest release, open needs-human issues) and resolver spend from the
durable sinks the sync writes it to (sync commit bodies and needs-human issue
bodies). Best-effort: any field it cannot resolve renders as an em dash rather
than failing the digest.
"""
import json
import os
import re
import subprocess
from datetime import datetime, timezone
from pathlib import Path

REPO = os.environ["GITHUB_REPOSITORY"]
TITLE = "\N{FORK AND KNIFE} fleet status"
BOT = "github-actions[bot]"
SPEND = re.compile(r"\$([0-9]+(?:\.[0-9]+)?)")


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
        "api", f"repos/{REPO}/actions/workflows/{tool}.yml/runs",
        "--jq", ".workflow_runs[0] // empty | {conclusion, status, html_url, created_at}",
        default=None,
    )


def latest_release(tool):
    return gh_json(
        "api", f"repos/{REPO}/releases",
        "--jq", f'[.[] | select(.tag_name | startswith("{tool}-v"))][0] // empty '
                "| {tag_name, html_url}",
        default=None,
    )


def open_issues():
    return gh_json(
        "issue", "list", "--repo", REPO, "--state", "open", "--author", BOT,
        "--limit", "200", "--json", "number,title,url,body",
        default=[],
    )


def last_spend(tool):
    body = subprocess.run(
        ["git", "log", "-1", "--format=%B", "--", f"{tool}/.fork/lock.json"],
        capture_output=True, text=True,
    ).stdout
    hits = SPEND.findall(body)
    return f"${hits[-1]}" if hits else "—"


def row(tool, issues):
    run = last_run(tool)
    rel = latest_release(tool)
    mine = [i for i in issues if tool.lower() in i["title"].lower()]

    if run:
        state = run["conclusion"] or run["status"] or "—"
        result = f"[{state}]({run['html_url']})"
    else:
        result = "—"
    release = f"[{rel['tag_name']}]({rel['html_url']})" if rel else "—"
    escal = " ".join(f"[#{i['number']}]({i['url']})" for i in mine) or "—"
    return f"| `{tool}` | {result} | {release} | {last_spend(tool)} | {escal} |"


def build_body():
    tools = forks()
    stamp = datetime.now(timezone.utc).strftime("%Y-%m-%d %H:%M UTC")
    if not tools:
        return (
            f"# {TITLE}\n\nNo forks in the fleet yet.\n\n"
            f"_Updated {stamp} · run {os.environ.get('GITHUB_RUN_ID', '?')}._\n"
        )
    issues = open_issues()
    lines = [
        f"# {TITLE}",
        "",
        "| fork | last run | latest release | last spend | needs-human |",
        "| --- | --- | --- | --- | --- |",
        *(row(t, issues) for t in tools),
        "",
        f"_Updated {stamp} · run {os.environ.get('GITHUB_RUN_ID', '?')}._",
    ]
    return "\n".join(lines) + "\n"


def upsert(body):
    existing = gh_json(
        "issue", "list", "--repo", REPO, "--state", "open", "--author", BOT,
        "--search", "in:title fleet status", "--json", "number,title", "--limit", "20",
        default=[],
    )
    for i in existing:
        if i["title"] == TITLE:
            gh("issue", "edit", str(i["number"]), "--repo", REPO, "--body", body, check=True)
            print(f"updated issue #{i['number']}")
            return
    out = gh("issue", "create", "--repo", REPO, "--title", TITLE, "--body", body, check=True)
    num = out.strip().rstrip("/").split("/")[-1]
    gh("issue", "pin", num, "--repo", REPO)  # best-effort; capped at 3 pins
    print(f"created + pinned issue #{num}")


if __name__ == "__main__":
    upsert(build_body())
