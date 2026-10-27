param(
    [ValidatePattern('^previewforge-test-[a-z0-9][a-z0-9-]{0,31}$')]
    [string]$ClusterName = ('previewforge-test-' + [guid]::NewGuid().ToString('N').Substring(0, 8))
)
$ErrorActionPreference = 'Stop'
Set-Location (Split-Path -Parent $PSScriptRoot)
$taskRoot = (Get-Location).Path
$env:GOMODCACHE = Join-Path $taskRoot '.cache\gomod'
$env:GOCACHE = Join-Path $taskRoot '.cache\go-build'
$env:GOBIN = Join-Path $taskRoot 'bin'
$taskConfig = Join-Path $taskRoot "bin\runs\$ClusterName\kubeconfig"
$taskKind = Join-Path $taskRoot 'bin\kind.exe'
$taskContext = "kind-$ClusterName"
function Invoke-Checked {
    param([scriptblock]$Command)
    & $Command
    if ($LASTEXITCODE -ne 0) { throw "Command failed with exit code $LASTEXITCODE" }
}
foreach ($toolName in @('go', 'docker', 'kubectl')) {
    if (-not (Get-Command $toolName -ErrorAction SilentlyContinue)) { throw "Missing prerequisite: $toolName" }
}
# Refuse remote Docker endpoints as well as external Kubernetes contexts.
if ($env:DOCKER_HOST -and $env:DOCKER_HOST -notmatch '^(unix|npipe)://') { throw 'A local Docker endpoint is required; remote DOCKER_HOST is refused.' }
$taskDockerEndpoint = & docker context inspect --format '{{.Endpoints.docker.Host}}'
if ($LASTEXITCODE -ne 0) { throw 'Failed to inspect Docker context' }
if ([string]$taskDockerEndpoint -notmatch '^(unix|npipe)://') { throw 'A local Docker context is required; remote endpoints are refused.' }
# Fail before installing tools or changing a cluster when the Docker daemon is unavailable.
Invoke-Checked { docker info }
New-Item -ItemType Directory -Path $env:GOBIN -Force | Out-Null
if (-not (Test-Path -LiteralPath $taskKind)) {
    Invoke-Checked { go install sigs.k8s.io/kind@v0.33.0 }
}
$taskClusters = @(& $taskKind get clusters)
if ($LASTEXITCODE -ne 0) { throw 'Failed to list kind clusters' }
# Cluster name alone cannot prove it is disposable. Never adopt an existing cluster.
if ($taskClusters -contains $ClusterName) { throw "Refusing to reuse existing cluster '$ClusterName'. Choose a fresh previewforge-test-* name." }
if (Test-Path -LiteralPath (Split-Path -Parent $taskConfig)) { throw "Refusing to overwrite an existing run directory. Choose a fresh cluster name." }
New-Item -ItemType Directory -Path (Split-Path -Parent $taskConfig) -Force | Out-Null
Invoke-Checked { & $taskKind create cluster --name $ClusterName --image 'kindest/node:v1.37.0@sha256:a1ed56cfb0e7b93589bdf97c8cd566405a265939e3620fc4f5de89adff580ae5' --kubeconfig $taskConfig --wait 120s }
Copy-Item -LiteralPath $taskConfig -Destination (Join-Path $taskRoot 'bin\kind.kubeconfig') -Force
$taskImage = "previewforge:$ClusterName"
$taskOverlay = Split-Path -Parent $taskConfig
[System.IO.File]::WriteAllLines((Join-Path $taskOverlay 'kustomization.yaml'), @(
    'resources:', '  - ../../../config/default', 'images:', '  - name: previewforge', "    newTag: $ClusterName"
), (New-Object System.Text.UTF8Encoding $false))
Invoke-Checked { docker build -t $taskImage . }
Invoke-Checked { & $taskKind load docker-image $taskImage --name $ClusterName }
Invoke-Checked { kubectl --kubeconfig $taskConfig --context $taskContext apply -k $taskOverlay }
Invoke-Checked { kubectl --kubeconfig $taskConfig --context $taskContext wait --for=condition=Established crd/previewenvironments.platform.previewforge.dev --timeout=60s }
Invoke-Checked { kubectl --kubeconfig $taskConfig --context $taskContext -n previewforge-system rollout status deployment/previewforge-controller-manager --timeout=120s }
Invoke-Checked { kubectl --kubeconfig $taskConfig --context $taskContext apply -f config/samples/platform_v1alpha1_previewenvironment.yaml }
Invoke-Checked { kubectl --kubeconfig $taskConfig --context $taskContext wait --for=condition=Ready previewenvironment/pr-42 --timeout=180s }
Invoke-Checked { kubectl --kubeconfig $taskConfig --context $taskContext get previewenvironment/pr-42 }
Write-Host "Cluster: $ClusterName; context: $taskContext; kubeconfig: $taskConfig"
Write-Host "To remove only this test cluster: .\bin\kind.exe delete cluster --name $ClusterName"
