"""Build, provision and exercise the wasmhooks-m3 kind cluster."""

import argparse
import base64
from contextlib import ExitStack, contextmanager
import json
import os
from pathlib import Path
from queue import Queue
import re
import secrets
import subprocess
import sys
from tempfile import TemporaryDirectory
from threading import Thread

from scenario import CLUSTER, CONTEXT, NAMESPACE, ROOT, schedulable_nodes, utc


HERE = Path(__file__).resolve().parent
NODE_IMAGE = ("kindest/node:v1.34.3@sha256:"
              "08497ee19eace7b4b5348db5c6a1591d7752b164530a36f855cb0f2bdcbadd48")
LOCAL = {name: f"{CLUSTER}/{name}:local" for name in ("control-plane", "demo-shop", "dataplane")}
STORAGE = ("postgres:17", "chainguard/minio:latest")
KUBE = ["kubectl", "--context", CONTEXT, "--namespace", NAMESPACE]


def command(args, *, env=None, capture=False, stdin=None):
    result = subprocess.run(args, cwd=ROOT, env=env, input=stdin, check=True, text=True,
                            encoding="utf-8", stdout=subprocess.PIPE if capture else None)
    return result.stdout.strip() if capture else None


def platform_credentials():
    database = secrets.token_hex(24)
    internal = secrets.token_hex(32)
    storage = secrets.token_hex(24)
    return {"CP_DATABASE_URL": f"postgresql+asyncpg://wasmhooks:{database}@postgres:5432/controlplane",
            "POSTGRES_PASSWORD": database, "CP_INTERNAL_TOKEN": internal, "WASMHOOKS_INTERNAL_TOKEN": internal,
            "CP_TENANT_JWT_SECRET": secrets.token_hex(48), "CP_S3_ACCESS_KEY": "m3-storage",
            "CP_S3_SECRET_KEY": storage, "MINIO_ROOT_PASSWORD": storage, "WASMHOOKS_S3_SECRET_KEY": storage}


def get_or_create_secret(env, name, generate):
    existing = command([*KUBE, "get", "secret", name, "--ignore-not-found", "-o", "json"], env=env, capture=True)
    if existing:
        return {k: base64.b64decode(v).decode() for k, v in json.loads(existing)["data"].items()}
    values = generate()
    manifest = {"apiVersion": "v1", "kind": "Secret", "metadata": {"name": name},
                "type": "Opaque", "stringData": values}
    # Pass secret material solely on stdin. kubectl create also avoids the plaintext
    # last-applied annotation that a client-side apply of stringData would create.
    command([*KUBE, "create", "-f", "-"], env=env, stdin=json.dumps(manifest))
    return values


def build_and_load(args, env):
    if not args.skip_build:
        for name, image in LOCAL.items():
            command(["docker", "build", "-t", image, str(ROOT / name)])
    if not args.skip_load:
        for image in STORAGE:
            if subprocess.run(["docker", "image", "inspect", image], stdout=subprocess.DEVNULL,
                              stderr=subprocess.DEVNULL).returncode:
                command(["docker", "pull", image])
        command(["kind", "load", "docker-image", "--name", CLUSTER, *LOCAL.values(), *STORAGE], env=env)


@contextmanager
def forward(service, port, env):
    process = subprocess.Popen([*KUBE, "port-forward", "--address", "127.0.0.1", "svc/" + service, f":{port}"],
                               env=env, stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
                               text=True, encoding="utf-8")
    ready = Queue()

    def drain():
        last = ""
        for line in process.stdout:
            last = line.rstrip()
            match = re.fullmatch(r"Forwarding from 127\.0\.0\.1:(\d+) -> \d+", last)
            if match:
                ready.put("http://127.0.0.1:" + match[1])
        ready.put(RuntimeError(f"port-forward {service}:{port} exited: {last}"))

    reader = Thread(target=drain, daemon=True)
    reader.start()
    try:
        url = ready.get(timeout=45)
        if isinstance(url, Exception):
            raise url
        yield url
    finally:
        if process.poll() is None:
            process.terminate()
        try:
            process.wait(timeout=5)
        except subprocess.TimeoutExpired:
            process.kill()
            process.wait()
        reader.join()
        process.stdout.close()


def provision(args, env):
    kconfig = Path(env["KUBECONFIG"])
    if CLUSTER not in command(["kind", "get", "clusters"], capture=True, env=env).splitlines():
        command(["kind", "create", "cluster", "--name", CLUSTER, "--image", NODE_IMAGE, "--wait", "180s",
                 "--kubeconfig", str(kconfig)], env=env)
    else:
        # Never change the user's current kubectl context.
        kconfig.write_text(command(["kind", "get", "kubeconfig", "--name", CLUSTER], capture=True, env=env)
                           + "\n", encoding="utf-8")
    schedulable_nodes(json.loads(command([*KUBE, "get", "nodes", "-o", "json"], env=env, capture=True))["items"])
    build_and_load(args, env)
    namespace = {"apiVersion": "v1", "kind": "Namespace", "metadata": {"name": NAMESPACE}}
    command([*KUBE, "apply", "-f", "-"], env=env, stdin=json.dumps(namespace))
    platform = get_or_create_secret(env, "m3-platform", platform_credentials)
    command([*KUBE, "apply", "-f", str(HERE / "storage.yaml")], env=env)

    def rollout(name):
        command([*KUBE, "rollout", "status", "deployment/" + name, "--timeout=240s"], env=env)

    rollout("postgres")
    rollout("minio")
    manifest = (HERE / "stack.yaml").read_text(encoding="utf-8")
    image_ids = {}
    for name, tag in LOCAL.items():
        # The template annotation restarts a running pod only if the local image changed.
        image_id = command(["docker", "image", "inspect", "--format", "{{.Id}}", tag], capture=True)
        image_ids[name] = image_id
        manifest = manifest.replace(f"__{name.upper().replace('-', '_')}_IMAGE_ID__", image_id)
    command([*KUBE, "apply", "-f", "-"], env=env, stdin=manifest)
    rollout("control-plane")

    def installation_key():
        token = command([*KUBE, "exec", "deployment/control-plane", "-c", "control-plane", "--",
                         "controlplane", "apikey", "create", "--name", "kind-m3-" + secrets.token_hex(4)],
                        env=env, capture=True)
        if not re.fullmatch(r"whk_[A-Za-z0-9]{32}", token):
            raise ValueError("controlplane apikey create returned an invalid token")
        return {"SHOP_API_KEY": token}

    installation = get_or_create_secret(env, "m3-installation", installation_key)
    for name in ("executor", "gateway", "demo-shop"):
        rollout(name)
    env.update(SHOP_API_KEY=installation["SHOP_API_KEY"], WASMHOOKS_INTERNAL_TOKEN=platform["CP_INTERNAL_TOKEN"])
    return image_ids


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--skip-build", action="store_true", help="reuse locally built application images")
    parser.add_argument("--skip-load", action="store_true", help="reuse images already loaded into kind")
    parser.add_argument("--output", type=Path, default=ROOT / "docs/reviews/m3-kind-report.json")
    for option in ("rps", "extra-pairs", "phase-seconds", "series-rounds", "convergence-seconds"):
        parser.add_argument("--" + option, type=int)
    args = parser.parse_args()
    output = args.output.resolve()
    with TemporaryDirectory(prefix="wasmhooks-m3-") as directory, ExitStack() as stack:
        env = {**os.environ, "KUBECONFIG": str(Path(directory) / "kubeconfig")}
        staged = Path(directory) / "report.json"
        report = {"result": "FAIL", "provisioning_started_at": utc(), "cluster": CONTEXT}
        try:
            report["application_images"] = provision(args, env)
            invoke = [sys.executable, "-B", str(HERE / "scenario.py"), "--output", str(staged)]
            for option, service, port in (("cp", "control-plane", 8000), ("gateway", "gateway", 8080),
                                          ("internal", "gateway", 8081), ("shop", "demo-shop", 8000)):
                url = stack.enter_context(forward(service, port, env))
                invoke.extend(["--" + option + "-url", url])
            for name, value in vars(args).items():
                if name not in {"skip_build", "skip_load", "output"} and value is not None:
                    invoke.extend(["--" + name.replace("_", "-"), str(value)])
            status = subprocess.run(invoke, cwd=ROOT, env=env).returncode
            report.update(json.loads(staged.read_text(encoding="utf-8")))
        except (Exception, KeyboardInterrupt) as exc:
            status = 1
            report.update(error=f"{type(exc).__name__}: {exc}", ended_at=utc())
            print(report["error"], file=sys.stderr)
        output.parent.mkdir(parents=True, exist_ok=True)
        output.write_text(json.dumps(report, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")
        print(f"{report['result']} {output}", flush=True)
        return status


if __name__ == "__main__":
    sys.exit(main())
