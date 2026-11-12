# Contributing to NodeCore

## Helm Chart Schema Guidelines

1. Always update `values.schema.json` when modifying `values.yaml`.
2. CI will run `helm lint chart/nodecore` in PR workflow.
3. PRs changing chart files require review from the CODEOWNERS team `@drpcorg/infra`
as defined in `.github/CODEOWNERS`.

## Signed Commits

The repository requires signed commits: every commit pushed to a branch covered by the rule must
carry a signature that GitHub can verify. GitHub rejects a push that contains an unsigned commit, or a
commit whose signature shows as "Unverified".

### Why

Git takes the author name and email from local config, and anyone can set them to anything, so a
commit can claim any author. A signature ties the commit to a key registered on a GitHub account.
nodecore sits in the request path of RPC traffic and holds client API keys and upstream
credentials, so we want proof of who wrote every change that reaches it.

### Setup

SSH signing is the simplest option if you already push over SSH. GPG and S/MIME also work.

1. Tell git to sign with your SSH key and to sign every commit:

   ```shell
   git config --global gpg.format ssh
   git config --global user.signingkey ~/.ssh/id_ed25519.pub
   git config --global commit.gpgsign true
   git config --global tag.gpgsign true
   ```

2. Add the same public key on GitHub under **Settings → SSH and GPG keys → New SSH key** with
   **Key type: Signing Key**. An authentication key does not count, even if it is the same key; add
   it a second time as a signing key.
3. Make sure `git config user.email` matches a verified email on your GitHub account. A valid
   signature with an unverified or unknown email still shows as "Unverified".

To use GPG instead, set `user.signingkey` to your key ID, leave `gpg.format` unset, and upload the
public key (`gpg --armor --export <key-id>`) under **New GPG key**. See GitHub's
[commit signature verification docs](https://docs.github.com/en/authentication/managing-commit-signature-verification)
for details.

### Check your commits

```shell
git log --show-signature -1
```

For SSH signatures, git can only check them locally if `gpg.ssh.allowedSignersFile` is set; the
"Verified" badge on GitHub is what counts. Merge and squash commits created from the PR page, and
Dependabot's commits, are signed by GitHub and need nothing from you.

### Fix unsigned commits on a branch

Re-sign every commit on your branch since `main` and force-push:

```shell
git rebase --force-rebase --gpg-sign origin/main
git push --force-with-lease
```
