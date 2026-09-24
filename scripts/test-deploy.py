#!/usr/bin/env python3
"""Run without SSH/root: retention safety, cache invalidation, and SSH reuse."""
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile

REPO = Path(__file__).resolve().parent.parent


def run(*args, **kwargs):
    return subprocess.run(args, text=True, capture_output=True, check=True, **kwargs)


with tempfile.TemporaryDirectory(prefix="tiki-script-test-") as temporary:
    root = Path(temporary)
    releases = root / "releases"
    backups = root / "backups"
    releases.mkdir()
    backups.mkdir()
    names = [f"202609{i:02d}T120000Z-abcdefgh" for i in range(1, 13)]
    for name in names:
        (releases / name).mkdir()
        (releases / name / "tiki").write_text("binary")
        (backups / f"{name}.db").write_text("snapshot")
    # Even old rollback targets and their snapshots must survive pruning.
    (root / "current").symlink_to(releases / names[-1])
    (root / "previous").symlink_to(releases / names[0])
    (releases / "manual").mkdir()
    (backups / "manual.db").write_text("manual snapshot")
    (releases / "20260913T120000Z-abcdefgh").symlink_to(root)
    command = ["bash", str(REPO / "scripts/prune-deployments.sh"), str(root), str(backups)]
    run(*command)
    assert {p.name for p in releases.iterdir() if not p.is_symlink()} == {names[0], names[-1], "manual"}
    expected = {f"{n}.db" for n in names[-7:] + names[:1]} | {"manual.db"}
    assert {p.name for p in backups.iterdir()} == expected
    run(*command)  # Idempotent; also preserves the symlink and manual files.
    (root / "current").unlink()
    (root / "current").symlink_to(releases / names[-1] / "nested")
    (releases / names[-1] / "nested").mkdir()
    assert subprocess.run(command, capture_output=True).returncode != 0
    assert (releases / names[-1] / "tiki").exists()

with tempfile.TemporaryDirectory(prefix="tiki-build-test-") as temporary:
    root = Path(temporary)
    (root / "scripts").mkdir()
    (root / "frontend/src").mkdir(parents=True)
    (root / "bin").mkdir()
    source = root / "frontend/src/app.js"
    source.write_text("initial")
    script = root / "scripts/build-frontend.mjs"
    shutil.copyfile(REPO / "scripts/build-frontend.mjs", script)
    npm = root / "bin/npm"
    npm.write_text('#!/bin/sh\n[ -z "${FAIL_BUILD:-}" ] || exit 1\nmkdir -p frontend/dist\necho built > frontend/dist/index.html\necho build >> builds\n')
    npm.chmod(0o755)
    env = dict(os.environ, PATH=f"{root / 'bin'}:{os.environ['PATH']}")
    def build(**extra):
        run("node", str(script), env=dict(env, **extra))
    build()
    build()
    assert (root / "builds").read_text().count("build") == 1
    old_stat = source.stat()
    source.write_text("changed")
    os.utime(source, ns=(old_stat.st_atime_ns, old_stat.st_mtime_ns))
    build()  # Contents, not mtimes.
    source.unlink()
    build()  # Deleted sources.
    (root / "frontend/dist/index.html").unlink()
    build()  # Missing outputs.
    (root / "frontend/.env.production").write_text("VITE_LABEL=production")
    stamp = (root / ".dev/frontend-build.json").read_text()
    failed = subprocess.run(["node", str(script)], env=dict(env, FAIL_BUILD="1"), capture_output=True)
    assert failed.returncode != 0
    assert (root / ".dev/frontend-build.json").read_text() == stamp
    build()  # Failed builds must not mark new input as cached.
    build(VITE_LABEL="override")
    assert (root / "builds").read_text().count("build") == 6

with tempfile.TemporaryDirectory(prefix="tiki-cli-build-test-") as temporary:
    root = Path(temporary)
    (root / "scripts").mkdir()
    (root / "bin").mkdir()
    shutil.copyfile(REPO / "scripts/build-cli.sh", root / "scripts/build-cli.sh")
    go = root / "bin/go"
    go.write_text('#!/bin/sh\nwhile [ "$1" != -o ]; do shift; done\nshift\nprintf "%s" "${CLI_VERSION:-first}-$GOOS-$GOARCH" > "$1"\n')
    go.chmod(0o755)
    env = dict(os.environ, PATH=f"{root / 'bin'}:{os.environ['PATH']}")
    command = ["sh", str(root / "scripts/build-cli.sh")]
    run(*command, env=env)
    archive = root / "internal/httpapi/dist/linux-amd64.gz"
    before = archive.stat().st_mtime_ns
    assert run(*command, env=env).stdout.count("unchanged") == 4
    assert archive.stat().st_mtime_ns == before
    archive.write_bytes(b"corrupt")
    assert "Bundling CLI for linux-amd64" in run(*command, env=env).stdout
    # A changed source binary must refresh all four archives.
    assert run(*command, env=dict(env, CLI_VERSION="second")).stdout.count("Bundling") == 4

with tempfile.TemporaryDirectory(prefix="tiki-ssh-test-") as temporary:
    root = Path(temporary)
    for directory in ["scripts", "bin", "deploy"]:
        (root / directory).mkdir()
    for name in ["deploy.sh", "install-server.sh", "prune-deployments.sh"]:
        shutil.copyfile(REPO / "scripts" / name, root / "scripts" / name)
    shutil.copyfile(REPO / "deploy/tiki.service", root / "deploy/tiki.service")
    # Simulate a server that refuses every fresh authentication after the first.
    # All subsequent sessions must request the same socket and disable prompts.
    ssh = root / "bin/ssh"
    ssh.write_text('''#!/usr/bin/env python3
import hashlib, json, os, pathlib, sys, tarfile
root = pathlib.Path(os.environ["TEST_ROOT"])
args = sys.argv[1:]
with (root / "calls").open("a") as output: output.write(json.dumps(args) + "\\n")
socket = next(arg.split("=", 1)[1] for arg in args if arg.startswith("ControlPath="))
state = root / "socket"
if "-Nf" in args:
    assert not state.exists()
    assert "ControlMaster=yes" in args
    state.write_text(socket)
elif "-O" in args:
    assert state.read_text() == socket
    state.unlink()
else:
    assert state.read_text() == socket
    assert "BatchMode=yes" in args
    assert "ControlMaster=no" in args
    assert "ProxyCommand=false" in args
    if args[-1] == "uname -sm": print("Linux x86_64")
    elif "sha256sum" in args[-1] and os.environ.get("UNCHANGED") == "1":
        for path, name in [(root / ".dev/build/server-linux-amd64", "tiki"), (root / "deploy/tiki.service", "tiki.service")]:
            print(hashlib.sha256(path.read_bytes()).hexdigest() + "  " + name)
    elif "tar -xzf" in args[-1]:
        with tarfile.open(fileobj=sys.stdin.buffer, mode="r|gz") as archive:
            names = {entry.name for entry in archive}
        assert {"SHA256SUMS", "install-server.sh", "prune-deployments.sh"} <= names
        assert ("tiki" not in names) == (os.environ.get("UNCHANGED") == "1")
        sys.exit(int(os.environ.get("FAIL_UPLOAD", "0")))
''')
    ssh.chmod(0o755)
    for name, content in {
        "make": "#!/bin/sh\nexit 0\n",
        "go": '#!/bin/sh\nwhile [ "$1" != -o ]; do shift; done\nshift\nprintf binary > "$1"\n',
    }.items():
        file = root / "bin" / name
        file.write_text(content)
        file.chmod(0o755)
    env = dict(os.environ, TEST_ROOT=str(root), PATH=f"{root / 'bin'}:{os.environ['PATH']}")
    for failure, unchanged in [("0", "0"), ("0", "1"), ("1", "0")]:
        result = subprocess.run(["bash", str(root / "scripts/deploy.sh"), "test-host"], env=dict(env, FAIL_UPLOAD=failure, UNCHANGED=unchanged), capture_output=True)
        assert result.returncode == int(failure), result.stderr
        assert not (root / "socket").exists(), "SSH master leaked on exit"
    calls = [json.loads(line) for line in (root / "calls").read_text().splitlines()]
    assert sum("-Nf" in args for args in calls) == 3

print("Deployment script checks passed.")
