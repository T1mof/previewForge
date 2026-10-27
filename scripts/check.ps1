param([switch]$Generate)
$ErrorActionPreference = 'Stop'
Set-Location (Split-Path -Parent $PSScriptRoot)
$taskRoot = (Get-Location).Path
$env:GOMODCACHE = Join-Path $taskRoot '.cache\gomod'
$env:GOCACHE = Join-Path $taskRoot '.cache\go-build'
$env:GOBIN = Join-Path $taskRoot 'bin'
function Invoke-Checked {
    param([scriptblock]$Command)
    & $Command
    if ($LASTEXITCODE -ne 0) { throw "Command failed with exit code $LASTEXITCODE" }
}
if ($Generate) {
    Invoke-Checked { go install sigs.k8s.io/controller-tools/cmd/controller-gen@v0.22.0 }
    Invoke-Checked { & "$env:GOBIN\controller-gen.exe" object 'paths=./api/v1alpha1' }
    Invoke-Checked { & "$env:GOBIN\controller-gen.exe" crd rbac:roleName=previewforge-manager-role 'paths=./api/v1alpha1;./internal/controller' output:crd:artifacts:config=config/crd/bases output:rbac:artifacts:config=config/rbac }
}
Invoke-Checked { gofmt -w api cmd internal }
Invoke-Checked { go vet ./... }
Invoke-Checked { go test ./... '-coverprofile=coverage.out' }
New-Item -ItemType Directory -Path $env:GOBIN -Force | Out-Null
Invoke-Checked { go build -trimpath -o "$env:GOBIN\manager.exe" ./cmd/manager }
Invoke-Checked { kubectl kustomize config/default | Out-File -FilePath "$env:GOBIN\install.yaml" -Encoding utf8 }
