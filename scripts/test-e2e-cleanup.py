"""Linux fault-injection tests for e2e cleanup; use fake CLIs, no Kubernetes."""
import json
import os
from pathlib import Path
import shutil
import signal
import subprocess
import sys
import tempfile
import time

ROOT = Path(__file__).resolve().parent.parent
TOKEN = "0123456789abcdef0123456789abcdef"
CLUSTER = "previewforge-test-cleanup"
SOURCE = r'''package main
import (
    "encoding/json"
    "fmt"
    "os"
    "path/filepath"
    "strings"
    "time"
)
func option(name string) string {
    for i,arg := range os.Args { if arg==name && i+1<len(os.Args) { return os.Args[i+1] } }
    return ""
}
func main() {
    name:=filepath.Base(os.Args[0]); args:=strings.Join(os.Args[1:], " ")
    file,err:=os.OpenFile(os.Getenv("PF_TEST_LOG"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
    if err!=nil { panic(err) }; json.NewEncoder(file).Encode(append([]string{name}, os.Args[1:]...)); file.Close()
    fail:=os.Getenv("PF_FAIL"); state:=os.Getenv("PF_STATE"); cluster:=os.Getenv("PREVIEWFORGE_TEST_CLUSTER")
    if name=="curl" {
        if fail=="demo-http" { os.Exit(17) }
        if err:=os.WriteFile(option("-o"), []byte("Welcome to nginx\n"), 0600); err!=nil { panic(err) }
    }
    if name=="docker" {
        if strings.HasPrefix(args,"context inspect") { fmt.Println(os.Getenv("PF_ENDPOINT")) }
        if args=="info" && fail=="docker-info" { os.Exit(17) }
        if strings.HasPrefix(args,"build") {
            if fail=="build" { os.Exit(17) }
            if fail=="signal" { time.Sleep(30*time.Second) }
        }
    }
    if name=="kind" {
        if args=="get clusters" {
            fmt.Println("unrelated-existing-cluster")
            if value,err:=os.ReadFile(state); err==nil { fmt.Println(string(value)) }
        }
        if strings.HasPrefix(args,"create cluster") {
            os.WriteFile(state, []byte(cluster), 0600)
            if fail=="create" { os.Exit(17) }
            os.WriteFile(option("--kubeconfig"), []byte("dedicated test config"), 0600)
        }
        if strings.HasPrefix(args,"delete cluster") {
            if fail=="cleanup" { os.Exit(19) }
            value,_:=os.ReadFile(state)
            if string(value)!=option("--name") { panic("wrong cluster delete") }
            os.Remove(state)
        }
    }
    if name=="kubectl" {
        if strings.Contains(args,"apply -k") && (fail=="apply" || fail=="cleanup") { os.Exit(17) }
        if strings.Contains(args,"get previewenvironment pr-42") {
            if fail=="e2e-setup" { os.Exit(17) }
            if strings.Contains(args,"status.namespace") { fmt.Println("forge-fixture") }
            if strings.Contains(args,"status.phase") { fmt.Println("Pending 0") }
            if strings.Contains(args,"status.url") { fmt.Println("http://localhost") }
        }
        if strings.Contains(args,"get namespaces,previewenvironments") && fail=="diagnostics" { os.Exit(17) }
    }
}'''


def calls_at(log):
    return [json.loads(line) for line in log.read_text().splitlines()] if log.exists() else []


def main():
    if sys.platform != "linux":
        raise SystemExit("Run in Linux/WSL: these tests exercise Bash and Linux signal handling.")
    cache = ROOT / ".cache" / "cleanup-guards"
    cache.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(dir=cache) as temp:
        work = Path(temp).resolve()
        assert work.is_relative_to(cache.resolve())
        source = work / "fake.go"
        source.write_text(SOURCE)
        executable = work / "fake"
        subprocess.run(["go", "build", "-o", str(executable), str(source)], check=True, cwd=ROOT)
        scenarios = ("cleanup-success", "missing-claim", "wrong-token", "changed-endpoint", "remote-endpoint",
                     "invalid-name", "existing-cluster", "docker-info", "create", "build", "apply",
                     "e2e-setup", "assertion", "diagnostics", "cleanup", "signal", "successful-body", "demo-http", "demo-success", "existing-run", "demo-existing-run")
        for scenario in scenarios:
            fixture = work / scenario
            scripts = fixture / "scripts"
            scripts.mkdir(parents=True)
            (fixture / "config" / "network-demo").mkdir(parents=True)
            shutil.copy2(ROOT / "config/network-demo/operator-patch.yaml", fixture / "config/network-demo/operator-patch.yaml")
            for script in ("kind-demo.sh", "install-calico.sh", "ingress-demo.sh", "kind-lifecycle.sh", "cleanup-kind.sh", "e2e.sh", "e2e-ingress.sh", "e2e-pr.sh", "e2e-metrics.sh", "e2e-guardrails.sh", "e2e-network.sh", "e2e-admission.sh", "e2e-stress.sh", "verify-e2e-result.sh"):
                shutil.copy2(ROOT / "scripts" / script, scripts / script)
            if scenario == "successful-body":
                # Exercise the successful EXIT path without pretending to run
                # Kubernetes assertions: replace only the body in this fixture.
                script = scripts / "e2e.sh"
                handler = script.read_text().split("bash scripts/kind-demo.sh\n", 1)[0]
                script.write_text(handler + "bash scripts/kind-demo.sh\nexit 0\n")
            tools = fixture / "tools"
            tools.mkdir()
            for tool in ("docker", "kind", "kubectl", "curl", "previewctl", "admissioncert"):
                shutil.copy2(executable, tools / tool)
            log = fixture / "calls.jsonl"
            state = fixture / "cluster-state"
            sentinel = fixture / "user-kubeconfig"
            sentinel.write_text("user context must stay untouched")
            directory = fixture / "bin" / "runs" / CLUSTER
            standalone = scenario in scenarios[:6]
            if standalone or scenario in ("existing-cluster", "existing-run", "demo-existing-run"):
                state.write_text(CLUSTER)
            if standalone and scenario != "missing-claim" or scenario in ("existing-run", "demo-existing-run"):
                directory.mkdir(parents=True)
                (directory / ".cluster-owner").write_text(TOKEN)
                (directory / ".docker-endpoint").write_text("unix:///var/run/docker.sock")
                (directory / ".docker-host").write_text("")
            env = os.environ.copy()
            env.update(PATH=str(tools) + os.pathsep + env["PATH"], KUBECONFIG=str(sentinel), DOCKER_HOST="", PREVIEWFORGE_INSTALLER="kustomize",
                       PREVIEWFORGE_DEMO_HTTP_PORT="",
                       PREVIEWFORGE_PREVIEWCTL="previewctl",
                       PREVIEWFORGE_TEST_CLUSTER="production" if scenario == "invalid-name" else CLUSTER,
                       PREVIEWFORGE_RUN_TOKEN="f" * 32 if scenario == "wrong-token" else TOKEN,
                       PF_TEST_LOG=str(log), PF_STATE=str(state), PF_FAIL=scenario,
                       PF_ENDPOINT=("unix:///different.sock" if scenario == "changed-endpoint" else
                                    "ssh://remote.example" if scenario == "remote-endpoint" else "unix:///var/run/docker.sock"))
            env["PREVIEWFORGE_KIND_CNI"] = "kindnet"
            command = ["bash", str(scripts / ("cleanup-kind.sh" if standalone else "ingress-demo.sh" if scenario.startswith("demo-") else "e2e.sh"))]
            if scenario == "signal":
                with (fixture / "output.txt").open("w") as output:
                    process = subprocess.Popen(command, cwd=fixture, env=env, stdout=output, stderr=output, start_new_session=True)
                    try:
                        deadline = time.monotonic() + 15
                        while not any(call[:2] == ["docker", "build"] for call in calls_at(log)):
                            if process.poll() is not None or time.monotonic() > deadline:
                                raise AssertionError("mock build was not reached")
                            time.sleep(0.05)
                        os.killpg(process.pid, signal.SIGTERM)
                        returncode = process.wait(timeout=15)
                    finally:
                        if process.poll() is None:
                            os.killpg(process.pid, signal.SIGKILL)
                            process.wait()
                result_output = (fixture / "output.txt").read_text()
            else:
                result = subprocess.run(command, cwd=fixture, env=env, text=True, capture_output=True, timeout=30)
                returncode, result_output = result.returncode, result.stdout + result.stderr
            calls = calls_at(log)
            deletes = [call for call in calls if call[:3] == ["kind", "delete", "cluster"]]
            protected = standalone and scenario != "cleanup-success" or scenario in ("existing-cluster", "docker-info", "existing-run", "demo-existing-run")
            if scenario == "demo-success":
                assert not deletes and state.read_text() == CLUSTER
                assert (directory / ".cluster-owner").read_text().strip() == TOKEN
                assert "PASS: real HTTP through Ingress" in (directory / "artifacts" / "demo.txt").read_text()
                assert "URL: http://localhost:8080" in (directory / "artifacts" / "demo.txt").read_text()
            elif protected:
                assert not deletes, calls
                assert not any(call[0] == "kubectl" for call in calls), calls
                if scenario != "docker-info":
                    assert state.read_text() == CLUSTER
            else:
                assert len(deletes) == 1, calls
                assert deletes[0] == ["kind", "delete", "cluster", "--name", CLUSTER, "--kubeconfig", str(directory / "kubeconfig")], deletes
                if scenario == "cleanup":
                    assert state.exists() and (directory / ".cluster-owner").exists()
                else:
                    assert not state.exists() and not (directory / ".cluster-owner").exists()
                    assert "absence verified" in (directory / "cleanup.txt").read_text()
            expected_success = scenario in ("cleanup-success", "missing-claim", "successful-body", "demo-success")
            assert (returncode == 0) == expected_success, result_output
            for call in calls:
                if call[0] == "kubectl":
                    assert Path(call[call.index("--kubeconfig") + 1]).resolve() == (directory / "kubeconfig").resolve(), call
                    assert call[call.index("--context") + 1] == "kind-" + CLUSTER, call
                if call[0] == "kind" and "--name" in call:
                    assert call[call.index("--name") + 1] == CLUSTER, call
            assert sentinel.read_text() == "user context must stay untouched"
            if scenario == "cleanup-success":
                # An always() fallback must be harmless after successful cleanup.
                again = subprocess.run(command, cwd=fixture, env=env, capture_output=True, timeout=15)
                assert again.returncode == 0
                assert len([c for c in calls_at(log) if c[:3] == ["kind", "delete", "cluster"]]) == 1
            if not standalone and not scenario.startswith("demo-") and scenario not in ("existing-cluster", "docker-info", "existing-run"):
                report = (directory / "artifacts" / "result.txt").read_text()
                assert ("E2E exit code: 0" in report) == (scenario == "successful-body")
            print(f"e2e cleanup: {scenario} PASS (fake CLI)")
            if scenario == "successful-body":
                gate = ["bash", str(scripts / "verify-e2e-result.sh")]
                checks = directory / "artifacts" / "checks.txt"
                # Only the isolation record exists: fake success must not be
                # accepted as a completed application lifecycle suite.
                assert subprocess.run(gate, cwd=fixture, env=env, capture_output=True).returncode != 0
                print("report gate: incomplete lifecycle rejected PASS (synthetic report)")
                checks.write_text("PASS: cluster-isolation: duplicate\n" * 21)
                assert subprocess.run(gate, cwd=fixture, env=env, capture_output=True).returncode != 0
                print("report gate: duplicate checks rejected PASS (synthetic report)")
                names = ("creation owners recreation drift restart http ingress pr-integration metrics guardrails network admission readiness finalizer ttl-active "
                         "ttl-downtime manual-delete foreign-resources cluster-isolation load faults").split()
                checks.write_text("".join(f"PASS: {name}: synthetic fixture\n" for name in names))
                assert subprocess.run(gate, cwd=fixture, env=env, capture_output=True).returncode != 0
                print("report gate: missing load/fault evidence rejected PASS (synthetic report)")
                (directory / "artifacts" / "stress-report.json").write_text('{"synthetic_fixture": true}')
                assert subprocess.run(gate, cwd=fixture, env=env, capture_output=True).returncode == 0
                print("report gate: complete evidence accepted PASS (synthetic report)")
                helm_env = dict(env, PREVIEWFORGE_INSTALLER="helm")
                checks.write_text("".join(f"PASS: {name}: synthetic fixture\n" for name in names) + "PASS: helm: synthetic fixture\n")
                rejected = subprocess.run(gate, cwd=fixture, env=helm_env, capture_output=True, text=True)
                assert rejected.returncode != 0 and "Missing Helm runtime evidence" in rejected.stderr
                print("report gate: missing Helm evidence rejected PASS (synthetic report)")
                checks.write_text("".join(f"PASS: {name}: synthetic fixture\n" for name in names))
                (directory / "artifacts" / "cleanup.txt").unlink()
                assert subprocess.run(gate, cwd=fixture, env=env, capture_output=True).returncode != 0
                print("report gate: missing cleanup rejected PASS (synthetic report)")


if __name__ == "__main__":
    main()
