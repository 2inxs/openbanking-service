import html
import json
import os
from pathlib import Path
import subprocess


def git(repo, *args, check=True, env=None):
    return subprocess.run(
        ["git", "-C", str(repo), *args],
        text=True,
        encoding="utf-8",
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        check=check,
        env=env,
    )


def api(endpoint, paginate=False):
    args = ["gh", "api", endpoint]
    if paginate:
        args.extend(["--paginate", "--slurp"])
    return json.loads(subprocess.check_output(args, text=True, encoding="utf-8"))


def eligible(pr, repository):
    return (
        pr["state"] == "open"
        and pr["base"]["ref"] == "main"
        and pr["base"]["repo"]["full_name"] == repository
        and pr["head"]["repo"] is not None
        and pr["head"]["repo"]["full_name"] == repository
        and pr["head"]["ref"] != "main"
    )


def update_branch(repo, number, branch, still_current):
    git(repo, "check-ref-format", f"refs/heads/{branch}")
    git(
        repo, "fetch", "--no-tags", "origin",
        "+refs/heads/main:refs/branch-sync/base",
        f"+refs/heads/{branch}:refs/branch-sync/head",
    )
    base = git(repo, "rev-parse", "refs/branch-sync/base").stdout.strip()
    head = git(repo, "rev-parse", "refs/branch-sync/head").stdout.strip()
    ancestor = git(repo, "merge-base", "--is-ancestor", base, head, check=False)
    if ancestor.returncode == 0:
        return "already up to date"
    if ancestor.returncode != 1:
        raise RuntimeError(ancestor.stderr)

    # Строим дерево слияния без checkout и без исполнения файлов из PR.
    merge = git(repo, "merge-tree", "--write-tree", head, base, check=False)
    if merge.returncode == 1:
        return "conflict: resolve manually"
    if merge.returncode != 0:
        raise RuntimeError(merge.stderr)
    tree = merge.stdout.splitlines()[0]
    identity = dict(
        os.environ,
        GIT_AUTHOR_NAME="github-actions[bot]",
        GIT_AUTHOR_EMAIL="41898282+github-actions[bot]@users.noreply.github.com",
        GIT_COMMITTER_NAME="github-actions[bot]",
        GIT_COMMITTER_EMAIL="41898282+github-actions[bot]@users.noreply.github.com",
    )
    commit = git(
        repo, "-c", "commit.gpgsign=false", "commit-tree", tree,
        "-p", head, "-p", base,
        "-m", f"chore: merge main into PR #{number}", env=identity,
    ).stdout.strip()
    if not still_current(head):
        return "changed or closed during update: skipped"

    # Новый коммит продолжает head; lease запрещает запись при любом изменении ветки.
    push = git(
        repo, "push", f"--force-with-lease=refs/heads/{branch}:{head}",
        "origin", f"{commit}:refs/heads/{branch}", check=False,
    )
    if push.returncode != 0:
        if "[rejected]" in push.stderr and (
            "stale info" in push.stderr or "non-fast-forward" in push.stderr
            or "fetch first" in push.stderr
        ):
            return "branch advanced during push: skipped"
        raise RuntimeError(push.stderr)
    return "updated; CI will run on the new commit"


def main():
    repository = os.environ["GITHUB_REPOSITORY"]
    target = os.environ.get("TARGET_PR", "")
    if target:
        pulls = [api(f"repos/{repository}/pulls/{int(target)}")]
    else:
        pages = api(
            f"repos/{repository}/pulls?state=open&base=main&per_page=100",
            paginate=True,
        )
        pulls = [pr for page in pages for pr in page]

    rows = []
    failed = False
    for pr in pulls:
        if not eligible(pr, repository):
            continue
        number, branch = pr["number"], pr["head"]["ref"]

        def still_current(head):
            current = api(f"repos/{repository}/pulls/{number}")
            return (
                eligible(current, repository)
                and current["head"]["ref"] == branch
                and current["head"]["sha"] == head
            )

        try:
            result = update_branch(Path.cwd(), number, branch, still_current)
        except (subprocess.CalledProcessError, RuntimeError) as exc:
            result = "update failed; see job log"
            detail = getattr(exc, "stderr", None) or str(exc)
            print(f"PR #{number}: {detail}", flush=True)
            failed = True
        print(f"PR #{number}: {result}", flush=True)
        rows.append(f"<li>PR #{number}: {html.escape(result)}</li>")

    summary = "<h2>PR branch updates</h2>\n<ul>" + "".join(rows) + "</ul>\n"
    if not rows:
        summary += "No eligible open PRs.\n"
    if os.environ.get("GITHUB_STEP_SUMMARY"):
        with open(os.environ["GITHUB_STEP_SUMMARY"], "a", encoding="utf-8") as out:
            out.write(summary)
    if failed:
        raise SystemExit(1)


if __name__ == "__main__":
    main()
