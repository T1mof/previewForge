"""Exercise kind demo command routing with fake CLIs; never contact Docker/Kubernetes."""
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile

ROOT = Path(__file__).resolve().parent.parent
WINDOWS = sys.platform == "win32"
SOURCE = r'''package main
import (
    "encoding/json"
    "fmt"
    "os"
    "path/filepath"
    "strings"
)
func main() {
    name := strings.TrimSuffix(filepath.Base(os.Args[0]), ".exe")
    f, err := os.OpenFile(os.Getenv("PF_TEST_LOG"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
    if err != nil { panic(err) }
    json.NewEncoder(f).Encode(append([]string{name}, os.Args[1:]...)); f.Close()
    if name == "docker" && len(os.Args)>2 && os.Args[1]=="context" && os.Args[2]=="inspect" {
        endpoint:=os.Getenv("PF_TEST_DOCKER_ENDPOINT"); if endpoint=="" { endpoint="unix:///var/run/docker.sock" }; fmt.Println(endpoint)
    }
    if name == "kind" && len(os.Args)>2 && os.Args[1]=="get" && os.Args[2]=="clusters" {
        if existing:=os.Getenv("PF_TEST_EXISTING"); existing!="" { fmt.Println(existing) }
    }
    if name == "kind" && len(os.Args)>2 && os.Args[1]=="create" && os.Args[2]=="cluster" {
        for i,arg := range os.Args { if arg=="--kubeconfig" && i+1<len(os.Args) {
            if err:=os.WriteFile(os.Args[i+1], []byte("fake dedicated config\n"), 0600); err!=nil { panic(err) }
        } }
    }
}'''


def option(call, name):
    return call[call.index(name) + 1]


def main():
    cache = ROOT / ".cache" / "script-guards"
    cache.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(dir=cache) as temp:
        work = Path(temp).resolve()
        assert work.is_relative_to(cache.resolve())
        source = work / "fake.go"
        source.write_text(SOURCE, encoding="utf-8")
        executable = work / ("fake.exe" if WINDOWS else "fake")
        subprocess.run(["go", "build", "-o", str(executable), str(source)], check=True, cwd=ROOT)
        scenarios = ("fresh", "existing", "existing_kubeconfig", "existing_run", "invalid", "remote_docker", "remote_docker_host")
        if not WINDOWS:
            scenarios += ("fresh_port", "invalid_port", "fresh_calico", "invalid_cni")
        for scenario in scenarios:
            fixture = work / scenario
            (fixture / "scripts").mkdir(parents=True)
            (fixture / "bin").mkdir()
            (fixture / "config" / "network-demo").mkdir(parents=True)
            shutil.copy2(ROOT / "config/network-demo/operator-patch.yaml", fixture / "config/network-demo/operator-patch.yaml")
            tools = fixture / "tools"
            tools.mkdir()
            suffix = ".exe" if WINDOWS else ""
            for tool in ("docker", "kind", "kubectl", "go", "admissioncert"):
                shutil.copy2(executable, tools / (tool + suffix))
            if WINDOWS:
                shutil.copy2(executable, fixture / "bin" / "kind.exe")
                shutil.copy2(executable, fixture / "bin" / "admissioncert.exe")
            script_name = "kind-demo.ps1" if WINDOWS else "kind-demo.sh"
            script = fixture / "scripts" / script_name
            shutil.copy2(ROOT / "scripts" / script_name, script)
            if not WINDOWS:
                shutil.copy2(ROOT / "scripts" / "kind-lifecycle.sh", fixture / "scripts" / "kind-lifecycle.sh")
                if scenario == "fresh_calico":
                    (fixture / "scripts/install-calico.sh").write_text("pf_assert_owned || exit 1\nk get nodes\n")
            cluster = "production" if scenario == "invalid" else "previewforge-test-check"
            kubeconfig = fixture / "bin" / "runs" / cluster / "kubeconfig"
            if scenario == "existing_kubeconfig":
                kubeconfig.parent.mkdir(parents=True)
                kubeconfig.write_text("do not overwrite", encoding="utf-8")
            if scenario == "existing_run":
                kubeconfig.parent.mkdir(parents=True)
                (kubeconfig.parent / "sentinel").write_text("previous run", encoding="utf-8")
            sentinel = fixture / "user-kubeconfig"
            sentinel.write_text("unrelated current context", encoding="utf-8")
            log = fixture / "calls.jsonl"
            env = os.environ.copy()
            env.update(PATH=str(tools) + os.pathsep + env["PATH"], KUBECONFIG=str(sentinel),
                       PF_TEST_LOG=str(log), PF_TEST_EXISTING=cluster if scenario == "existing" else "",
                       PF_TEST_DOCKER_ENDPOINT="ssh://remote.example" if scenario == "remote_docker" else "unix:///var/run/docker.sock",
                       DOCKER_HOST="ssh://remote.example" if scenario == "remote_docker_host" else "",
                       PREVIEWFORGE_DEMO_HTTP_PORT="8080" if scenario == "fresh_port" else "8080\nnodes: []" if scenario == "invalid_port" else "",
                       PREVIEWFORGE_TEST_CLUSTER=cluster)
            env["PREVIEWFORGE_KIND_CNI"] = "calico" if scenario == "fresh_calico" else "invalid" if scenario == "invalid_cni" else "kindnet"
            command = (["powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", str(script), "-ClusterName", cluster]
                       if WINDOWS else ["bash", str(script)])
            result = subprocess.run(command, cwd=fixture, env=env, text=True, capture_output=True, timeout=30)
            calls = [json.loads(line) for line in log.read_text(encoding="utf-8").splitlines()] if log.exists() else []
            assert sentinel.read_text(encoding="utf-8") == "unrelated current context"
            if scenario in ("fresh", "fresh_port", "fresh_calico"):
                assert result.returncode == 0, result.stdout + result.stderr
                kind_calls = [call for call in calls if call[0] == "kind"]
                assert any(call[1:3] == ["create", "cluster"] for call in kind_calls)
                for call in kind_calls:
                    if "--name" in call:
                        assert option(call, "--name") == cluster, call
                    assert call[1] not in ("delete", "export"), call
                kubectl_calls = [call for call in calls if call[0] == "kubectl"]
                assert kubectl_calls
                for call in kubectl_calls:
                    assert Path(option(call, "--kubeconfig")).resolve() == kubeconfig.resolve(), call
                    assert option(call, "--context") == "kind-" + cluster, call
                    if "apply" in call and "-k" in call:
                        assert Path(option(call, "-k")).resolve() == kubeconfig.parent.resolve(), call
                image = "previewforge:" + cluster
                assert any(call[0:2] == ["docker", "build"] and option(call, "-t") == image for call in calls)
                assert any(call[0:3] == ["kind", "load", "docker-image"] and call[3] == image for call in calls)
                assert "newTag: " + cluster in (kubeconfig.parent / "kustomization.yaml").read_text(encoding="utf-8-sig")
                create = next(call for call in kind_calls if call[1:3] == ["create", "cluster"])
                if scenario == "fresh_port":
                    config = Path(option(create, "--config"))
                    assert config.resolve().is_relative_to(kubeconfig.parent.resolve())
                    value = config.read_text()
                    assert 'listenAddress: "127.0.0.1"' in value and 'hostPort: 8080' in value and 'containerPort: 30080' in value
                elif scenario == "fresh_calico":
                    config = Path(option(create, "--config"))
                    assert config.resolve().is_relative_to(kubeconfig.parent.resolve())
                    assert 'disableDefaultCNI: true' in config.read_text()
                    assert 'podSubnet: "10.244.0.0/16"' in config.read_text()
                    assert "--wait" not in create, "CNI must install before waiting for node readiness"
                    assert any(call[0] == "kubectl" and call[-2:] == ["get", "nodes"] for call in calls)
                else:
                    assert "--config" not in create
            else:
                assert result.returncode != 0, "unsafe input was accepted"
                assert not any(call[0] == "kubectl" or call[0] == "kind" and call[1] != "get" for call in calls), calls
                if scenario == "existing_kubeconfig":
                    assert kubeconfig.read_text(encoding="utf-8") == "do not overwrite"
                if scenario == "existing_run":
                    assert (kubeconfig.parent / "sentinel").read_text(encoding="utf-8") == "previous run"
            print(f"{script_name}: {scenario} PASS")


if __name__ == "__main__":
    main()
