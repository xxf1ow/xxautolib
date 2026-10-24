[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][string]$Config,
    [Parameter(Mandatory = $true)][string]$Plan,
    [Parameter(Mandatory = $true)][ValidateSet('windows-x86_64')][string]$Platform,
    [Parameter(Mandatory = $true)][string]$WorkRoot
)
$ErrorActionPreference = 'Stop'
$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot '../..')).Path
$configPath = (Resolve-Path -LiteralPath $Config).Path
$planPath = (Resolve-Path -LiteralPath $Plan).Path
New-Item -ItemType Directory -Force -Path $WorkRoot | Out-Null
$WorkRoot = (Resolve-Path -LiteralPath $WorkRoot).Path
$configDocument = Get-Content -LiteralPath $configPath -Raw | ConvertFrom-Json
$planDocument = Get-Content -LiteralPath $planPath -Raw | ConvertFrom-Json
$platformPlan = @($planDocument.platforms | Where-Object id -eq $Platform)
if ($platformPlan.Count -ne 1) { throw "Plan must contain exactly one platform entry for $Platform." }

$cmakeVersion = [string]$configDocument.cmake.version
if ([string]::IsNullOrWhiteSpace($cmakeVersion) -or $configDocument.production.common.cmake_version -ne $cmakeVersion) {
    throw 'Production CMake version must match cmake.version.'
}
if ($configDocument.cmake.checksums_url -ne "https://github.com/Kitware/CMake/releases/download/v$cmakeVersion/cmake-$cmakeVersion-SHA-256.txt") {
    throw 'Configured CMake checksums URL does not match cmake.version.'
}
$asset = @($configDocument.cmake.assets | Where-Object platform -eq $Platform)
if ($asset.Count -ne 1 -or $asset[0].filename -ne "cmake-$cmakeVersion-windows-x86_64.zip" -or $asset[0].url -notmatch ('/cmake-' + [regex]::Escape($cmakeVersion) + '-windows-x86_64\.zip$')) {
    throw 'Configured Windows CMake asset is missing or inconsistent.'
}
$tools = Join-Path $WorkRoot 'ci-tools'
$bin = Join-Path $tools 'bin'
New-Item -ItemType Directory -Force -Path $bin | Out-Null
$archive = Join-Path $WorkRoot $asset[0].filename
Invoke-WebRequest -Uri $asset[0].url -OutFile $archive
$actual = (Get-FileHash -LiteralPath $archive -Algorithm SHA256).Hash.ToLowerInvariant()
if ($actual -ne $asset[0].sha256) { throw "CMake SHA256 mismatch: $actual" }
Expand-Archive -LiteralPath $archive -DestinationPath $tools -Force
$cmakeDirName = [IO.Path]::GetFileNameWithoutExtension([string]$asset[0].filename)
$cmakeDir = Join-Path $tools $cmakeDirName
if (-not (Test-Path -LiteralPath $cmakeDir -PathType Container)) { throw 'Pinned CMake archive has unexpected contents.' }
$cmakeCommand = Join-Path $cmakeDir 'bin/cmake.exe'
$cmakeVersion = & $cmakeCommand --version | Select-Object -First 1
if ($cmakeVersion -ne "cmake version $($configDocument.cmake.version)") { throw "CMake version does not match configuration: $cmakeVersion" }

$packages = @($platformPlan[0].system_dependencies.choco | Where-Object { $_ } | Sort-Object -Unique)
foreach ($package in $packages) {
    if ($package -notmatch '^[A-Za-z0-9.-]+$') { throw "Unsafe Chocolatey package name: $package" }
}
if ($packages.Count -gt 0) {
    choco install @packages --yes --no-progress --limit-output 2>&1 | ForEach-Object { [Console]::Error.WriteLine($_) }
    if ($LASTEXITCODE -ne 0) { throw "Chocolatey dependency installation failed: $LASTEXITCODE" }
}

$goSdkScript = Join-Path $repoRoot 'src/autolib-builder/go-sdk.cmake'
& $cmakeCommand -P $goSdkScript -- build -mod=readonly -trimpath -o (Join-Path $WorkRoot 'autolib-builder.exe') ./src/autolib-builder 2>&1 | ForEach-Object { [Console]::Error.WriteLine($_) }
if ($LASTEXITCODE -ne 0) { throw "Private Go SDK builder preparation failed: $LASTEXITCODE" }
& $cmakeCommand -P $goSdkScript -- build -mod=readonly -trimpath -o (Join-Path $WorkRoot 'autolib-importer.exe') ./src/autolib-importer 2>&1 | ForEach-Object { [Console]::Error.WriteLine($_) }
if ($LASTEXITCODE -ne 0) { throw "Private Go SDK importer preparation failed: $LASTEXITCODE" }
& $cmakeCommand -P $goSdkScript -- build -mod=readonly -trimpath -o (Join-Path $WorkRoot 'autolib-ci.exe') ./src/autolib-ci 2>&1 | ForEach-Object { [Console]::Error.WriteLine($_) }
if ($LASTEXITCODE -ne 0) { throw "Private Go SDK CI tool preparation failed: $LASTEXITCODE" }

if ($env:GITHUB_PATH) { Add-Content -LiteralPath $env:GITHUB_PATH -Value (Join-Path $cmakeDir 'bin') }
$result = [ordered]@{ cmake_command = $cmakeCommand; make_program = ''; budget = 0 }
$result | ConvertTo-Json -Compress
