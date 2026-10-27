"""Select a PR operation from fresh GitHub state without executing PR content."""
import ipaddress
import json
import os
from pathlib import Path
import re
import urllib.error
import urllib.request

TRUSTED = {"OWNER", "MEMBER", "COLLABORATOR"}
SHA = re.compile(r"(?:[a-f0-9]{40}|[a-f0-9]{64})\Z")
DNS = re.compile(r"[a-z0-9](?:[-a-z0-9]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[-a-z0-9]{0,61}[a-z0-9])?)*\Z")


def repo_matches(value, name, repo_id):
    return isinstance(value, dict) and value.get("id") == repo_id and str(value.get("full_name", "")).lower() == name.lower()


def decide(event, live, repository, repository_id, domain):
    if event.get("action") not in {"opened", "synchronize", "reopened", "closed"}:
        raise ValueError("unsupported PR event")
    number = event.get("number")
    if type(number) is not int or number <= 0 or number != live.get("number"):
        raise ValueError("PR number mismatch")
    if not repo_matches(event.get("repository"), repository, repository_id):
        raise ValueError("repository identity mismatch")
    old = event.get("pull_request") or {}
    for pr in (old, live):
        if not repo_matches((pr.get("base") or {}).get("repo"), repository, repository_id):
            raise ValueError("base repository mismatch")
        if not repo_matches((pr.get("head") or {}).get("repo"), repository, repository_id) or pr.get("author_association") not in TRUSTED:
            return {"action": "skip", "pr_number": str(number), "head_sha": "", "host": ""}
    head = (live.get("head") or {}).get("sha", "")
    if not SHA.fullmatch(head):
        raise ValueError("invalid live commit SHA")
    state = live.get("state")
    if state not in {"open", "closed"}:
        raise ValueError("unknown live PR state")
    action = "deploy"
    if event["action"] == "closed":
        action = "delete" if state == "closed" else "skip"
    elif state == "closed":
        action = "delete"
    elif (old.get("head") or {}).get("sha") != head:
        action = "skip"
    host = ""
    if action == "deploy":
        if not domain or "." not in domain or len(domain) > 253 or not DNS.fullmatch(domain):
            raise ValueError("a DNS base domain is required for deployment")
        try:
            ipaddress.ip_address(domain)
        except ValueError:
            pass
        else:
            raise ValueError("base domain cannot be an IP address")
        host = f"pr-{repository_id}-{number}.{domain}"
        if len(host) > 253 or not DNS.fullmatch(host):
            raise ValueError("generated preview hostname is invalid")
    return {"action": action, "pr_number": str(number), "head_sha": head, "host": host}


def main():
    repository = os.environ["GITHUB_REPOSITORY"]
    if not re.fullmatch(r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+", repository):
        raise ValueError("invalid repository name")
    repository_id = int(os.environ["GITHUB_REPOSITORY_ID"])
    if repository_id <= 0:
        raise ValueError("invalid repository ID")
    event = json.loads(Path(os.environ["GITHUB_EVENT_PATH"]).read_text())
    number = event.get("number")
    if type(number) is not int or number <= 0:
        raise ValueError("invalid PR number")
    token = os.environ["GH_TOKEN"]
    request = urllib.request.Request(
        f"https://api.github.com/repos/{repository}/pulls/{number}",
        headers={"Authorization": "Bearer " + token, "Accept": "application/vnd.github+json", "X-GitHub-Api-Version": "2022-11-28", "Cache-Control": "no-cache"},
    )
    try:
        with urllib.request.urlopen(request, timeout=30) as response:
            live = json.load(response)
    except urllib.error.HTTPError as exc:
        raise ValueError(f"cannot verify live PR state: GitHub HTTP {exc.code}") from None
    result = decide(event, live, repository, repository_id, os.environ.get("PREVIEWFORGE_BASE_DOMAIN", ""))
    with Path(os.environ["GITHUB_OUTPUT"]).open("a", encoding="utf-8") as output:
        for key, value in result.items():
            assert "\n" not in value and "\r" not in value
            output.write(f"{key}={value}\n")
    print(json.dumps(result))


if __name__ == "__main__":
    main()
