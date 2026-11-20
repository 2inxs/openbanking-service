from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import update_pr_branches as sync


class UpdateBranchTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        root = Path(self.temp.name)
        self.remote, self.repo = root / "remote.git", root / "repo"
        self.remote.mkdir()
        self.repo.mkdir()
        sync.git(self.remote, "init", "--bare", "--initial-branch=main")
        sync.git(self.repo, "init", "--initial-branch=main")
        sync.git(self.repo, "config", "user.name", "Test")
        sync.git(self.repo, "config", "user.email", "test@example.invalid")
        sync.git(self.repo, "config", "commit.gpgsign", "false")
        sync.git(self.repo, "config", "core.hooksPath", str(root / "no-hooks"))
        sync.git(self.repo, "remote", "add", "origin", str(self.remote))
        self.initial = self.commit("shared.txt", "initial\n")
        sync.git(self.repo, "push", "origin", "main")
        sync.git(self.repo, "branch", "feature")

    def commit(self, filename, content):
        (self.repo / filename).write_text(content, encoding="utf-8")
        sync.git(self.repo, "add", "--", filename)
        sync.git(self.repo, "commit", "-m", "test change")
        return sync.git(self.repo, "rev-parse", "HEAD").stdout.strip()

    def remote_head(self, branch):
        return sync.git(self.remote, "rev-parse", f"refs/heads/{branch}").stdout.strip()

    def prepare_branches(self, conflict=False):
        sync.git(self.repo, "switch", "feature")
        self.head = self.commit("shared.txt" if conflict else "feature.txt", "feature\n")
        sync.git(self.repo, "push", "origin", "feature")
        sync.git(self.repo, "switch", "main")
        self.base = self.commit("shared.txt" if conflict else "main.txt", "main\n")
        sync.git(self.repo, "push", "origin", "main")

    def test_merges_main_and_preserves_author_changes_without_checkout(self):
        self.prepare_branches()
        result = sync.update_branch(self.repo, 7, "feature", lambda sha: sha == self.head)
        self.assertEqual(result, "updated; CI will run on the new commit")
        merged = self.remote_head("feature")
        parents = sync.git(self.remote, "rev-list", "--parents", "-n", "1", merged).stdout.split()
        self.assertEqual(parents[1:], [self.head, self.base])
        self.assertEqual(sync.git(self.remote, "show", f"{merged}:feature.txt").stdout, "feature\n")
        self.assertEqual(sync.git(self.remote, "show", f"{merged}:main.txt").stdout, "main\n")
        self.assertEqual(sync.git(self.repo, "rev-parse", "HEAD").stdout.strip(), self.base)
        self.assertFalse((self.repo / "feature.txt").exists())

    def test_up_to_date_branch_does_not_get_another_commit(self):
        self.prepare_branches()
        sync.update_branch(self.repo, 7, "feature", lambda sha: True)
        first = self.remote_head("feature")
        self.assertEqual(
            sync.update_branch(self.repo, 7, "feature", lambda sha: True),
            "already up to date",
        )
        self.assertEqual(self.remote_head("feature"), first)

    def test_conflict_leaves_branch_untouched(self):
        self.prepare_branches(conflict=True)
        self.assertEqual(
            sync.update_branch(self.repo, 7, "feature", lambda sha: True),
            "conflict: resolve manually",
        )
        self.assertEqual(self.remote_head("feature"), self.head)

    def test_closed_or_changed_pr_is_not_pushed(self):
        self.prepare_branches()
        self.assertEqual(
            sync.update_branch(self.repo, 7, "feature", lambda sha: False),
            "changed or closed during update: skipped",
        )
        self.assertEqual(self.remote_head("feature"), self.head)

    def test_concurrent_commit_is_not_overwritten(self):
        self.prepare_branches()
        tree = sync.git(self.repo, "rev-parse", f"{self.head}^{{tree}}").stdout.strip()
        concurrent = sync.git(
            self.repo, "commit-tree", tree, "-p", self.head, "-m", "concurrent author commit",
        ).stdout.strip()

        def race(sha):
            sync.git(self.repo, "push", "origin", f"{concurrent}:refs/heads/feature")
            return True

        self.assertEqual(
            sync.update_branch(self.repo, 7, "feature", race),
            "branch advanced during push: skipped",
        )
        self.assertEqual(self.remote_head("feature"), concurrent)

    def test_concurrent_rewind_is_not_overwritten(self):
        self.prepare_branches()

        def rewind(sha):
            sync.git(self.remote, "update-ref", "refs/heads/feature", self.initial, self.head)
            return True

        self.assertEqual(
            sync.update_branch(self.repo, 7, "feature", rewind),
            "branch advanced during push: skipped",
        )
        self.assertEqual(self.remote_head("feature"), self.initial)

    def test_push_permission_failure_is_not_reported_as_success(self):
        self.prepare_branches()
        real_git = sync.git

        def denied(repo, *args, **kwargs):
            if args[0] == "push":
                return sync.subprocess.CompletedProcess(args, 128, "", "Permission denied")
            return real_git(repo, *args, **kwargs)

        with patch.object(sync, "git", side_effect=denied):
            with self.assertRaisesRegex(RuntimeError, "Permission denied"):
                sync.update_branch(self.repo, 7, "feature", lambda sha: True)
        self.assertEqual(self.remote_head("feature"), self.head)


class EligibilityTests(unittest.TestCase):
    def pr(self, **changes):
        pr = {
            "state": "open",
            "base": {"ref": "main", "repo": {"full_name": "owner/repo"}},
            "head": {"ref": "feature", "repo": {"full_name": "owner/repo"}},
        }
        pr.update(changes)
        return pr

    def test_same_repo_open_pr_is_eligible(self):
        self.assertTrue(sync.eligible(self.pr(), "owner/repo"))

    def test_fork_closed_deleted_or_retargeted_pr_is_skipped(self):
        cases = [
            self.pr(state="closed"),
            self.pr(head={"ref": "feature", "repo": {"full_name": "other/repo"}}),
            self.pr(head={"ref": "feature", "repo": None}),
            self.pr(base={"ref": "release", "repo": {"full_name": "owner/repo"}}),
            self.pr(head={"ref": "main", "repo": {"full_name": "owner/repo"}}),
        ]
        for pr in cases:
            with self.subTest(pr=pr):
                self.assertFalse(sync.eligible(pr, "owner/repo"))


if __name__ == "__main__":
    unittest.main()
