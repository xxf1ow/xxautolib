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

$asset = @($configDocument.cmake.assets | Where-Object platform -eq $Platform)
if ($configDocument.cmake.version -ne '3.31.12' -or $asset.Count -ne 1 -or $asset[0].filename -ne 'cmake-3.31.12-windows-x86_64.zip') {
    throw 'Pinned Windows CMake asset is missing or drifting.'
}
$tools = Join-Path $WorkRoot 'ci-tools'
$bin = Join-Path $tools 'bin'
New-Item -ItemType Directory -Force -Path $bin | Out-Null
$archive = Join-Path $WorkRoot $asset[0].filename
Invoke-WebRequest -Uri $asset[0].url -OutFile $archive
$actual = (Get-FileHash -LiteralPath $archive -Algorithm SHA256).Hash.ToLowerInvariant()
if ($actual -ne $asset[0].sha256) { throw "CMake SHA256 mismatch: $actual" }
Expand-Archive -LiteralPath $archive -DestinationPath $tools -Force
$cmakeDir = Join-Path $tools 'cmake-3.31.12-windows-x86_64'
if (-not (Test-Path -LiteralPath $cmakeDir -PathType Container)) { throw 'Pinned CMake archive has unexpected contents.' }
$cmakeCommand = Join-Path $cmakeDir 'bin/cmake.exe'
$cmakeVersion = & $cmakeCommand --version | Select-Object -First 1
if ($cmakeVersion -ne 'cmake version 3.31.12') { throw "Unexpected CMake version: $cmakeVersion" }

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
& $cmakeCommand -P $goSdkScript -- build -mod=readonly -trimpath -o (Join-Path $WorkRoot 'autolib-ci.exe') ./src/autolib-ci 2>&1 | ForEach-Object { [Console]::Error.WriteLine($_) }
if ($LASTEXITCODE -ne 0) { throw "Private Go SDK CI tool preparation failed: $LASTEXITCODE" }

$vswhere = Join-Path ${env:ProgramFiles(x86)} 'Microsoft Visual Studio/Installer/vswhere.exe'
if (-not (Test-Path -LiteralPath $vswhere)) { throw 'Visual Studio locator is unavailable.' }
$vsVersion = & $vswhere -latest -products '*' -property installationVersion
$vsPath = & $vswhere -latest -products '*' -property installationPath
if (-not $vsVersion -or -not $vsPath) { throw 'Visual Studio 2022 installation was not found.' }
[Console]::Error.WriteLine("VisualStudioVersion=$vsVersion")
[Console]::Error.WriteLine("VisualStudioPath=$vsPath")
$toolsetRoot = Join-Path $vsPath 'VC/Tools/MSVC'
$toolset = Get-ChildItem -LiteralPath $toolsetRoot -Directory | Sort-Object Name -Descending | Select-Object -First 1
if ($null -eq $toolset) { throw 'MSVC toolset installation was not found.' }
$compiler = Join-Path $toolset.FullName 'bin/Hostx64/x64/cl.exe'
if (-not (Test-Path -LiteralPath $compiler)) { throw "x64-hosted x64 MSVC compiler is missing: $compiler" }
[Console]::Error.WriteLine("MSVCToolset=$($toolset.Name)")
[Console]::Error.WriteLine("MSVCCompiler=$compiler")
& $compiler /Bv 2>&1 | ForEach-Object { [Console]::Error.WriteLine($_) }
$msbuild = Join-Path $vsPath 'MSBuild/Current/Bin/MSBuild.exe'
if (-not (Test-Path -LiteralPath $msbuild)) { throw 'Visual Studio MSBuild installation was not found.' }
$msbuildVersion = & $msbuild -version -nologo
[Console]::Error.WriteLine("MSBuildVersion=$msbuildVersion")
$windowsSdkInclude = Join-Path ${env:ProgramFiles(x86)} 'Windows Kits/10/Include'
if (-not (Test-Path -LiteralPath $windowsSdkInclude)) { throw 'Windows SDK installation was not found.' }
$windowsSdkVersions = @(Get-ChildItem -LiteralPath $windowsSdkInclude -Directory | Select-Object -ExpandProperty Name)
if ($windowsSdkVersions.Count -eq 0) { throw 'Windows SDK versions could not be identified.' }
[Console]::Error.WriteLine("WindowsSDKVersions=$($windowsSdkVersions -join ';')")
if (Get-Command choco.exe -ErrorAction SilentlyContinue) {
    choco list --limit-output 2>&1 | ForEach-Object { [Console]::Error.WriteLine($_) }
}
[Console]::Error.WriteLine("RunnerArchitecture=$env:PROCESSOR_ARCHITECTURE")
[Console]::Error.WriteLine("RunnerImage=$env:ImageOS/$env:ImageVersion")
if ($env:GITHUB_PATH) { Add-Content -LiteralPath $env:GITHUB_PATH -Value (Join-Path $cmakeDir 'bin') }
$result = [ordered]@{ cmake_command = $cmakeCommand; make_program = ''; budget = 0 }
$result | ConvertTo-Json -Compress
