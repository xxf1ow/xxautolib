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
if ([string]::IsNullOrWhiteSpace($cmakeVersion)) {
    throw 'cmake.version is required.'
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
Invoke-WebRequest -Uri $asset[0].url -OutFile $archive -MaximumRetryCount 2 -RetryIntervalSec 5
$actual = (Get-FileHash -LiteralPath $archive -Algorithm SHA256).Hash.ToLowerInvariant()
if ($actual -ne $asset[0].sha256) { throw "CMake SHA256 mismatch: $actual" }
Expand-Archive -LiteralPath $archive -DestinationPath $tools -Force
$cmakeDirName = [IO.Path]::GetFileNameWithoutExtension([string]$asset[0].filename)
$cmakeDir = Join-Path $tools $cmakeDirName
if (-not (Test-Path -LiteralPath $cmakeDir -PathType Container)) { throw 'Pinned CMake archive has unexpected contents.' }
$cmakeCommand = Join-Path $cmakeDir 'bin/cmake.exe'
$cmakeVersion = & $cmakeCommand --version | Select-Object -First 1
if ($cmakeVersion -ne "cmake version $($configDocument.cmake.version)") { throw "CMake version does not match configuration: $cmakeVersion" }

if (@($platformPlan[0].description.modules | Where-Object name -eq 'ffmpeg').Count -gt 0) {
    $msysRoot = Join-Path $env:SystemDrive 'msys64'
    $bash = Join-Path $msysRoot 'usr/bin/bash.exe'
    $make = Join-Path $msysRoot 'usr/bin/make.exe'
    if (-not (Test-Path -LiteralPath $bash)) { throw 'FFmpeg requires the MSYS2 Bash from the Windows CI image.' }
    if (-not (Test-Path -LiteralPath $make)) {
        & $bash -lc 'pacman -Sy --needed --noconfirm make'
        if ($LASTEXITCODE -ne 0) { throw 'MSYS2 GNU Make installation failed.' }
    }
    if (-not (Test-Path -LiteralPath $make)) { throw 'MSYS2 did not provide GNU Make.' }

    $vswhere = Join-Path ${env:ProgramFiles(x86)} 'Microsoft Visual Studio/Installer/vswhere.exe'
    $vsRoot = & $vswhere -latest -version '[17.0,18.0)' -products '*' -requires Microsoft.VisualStudio.Component.VC.Tools.x86.x64 -property installationPath
    if ($LASTEXITCODE -ne 0 -or -not $vsRoot) { throw 'FFmpeg requires Visual Studio 2022 C++ tools.' }
    $vsDevCmd = Join-Path $vsRoot 'Common7/Tools/VsDevCmd.bat'
    $compilerEnvironment = & cmd.exe /d /s /c ('""{0}" -no_logo -arch=x64 -host_arch=x64 && set"' -f $vsDevCmd)
    if ($LASTEXITCODE -ne 0) { throw 'Could not initialize MSVC x64 for FFmpeg.' }
    $values = @{}
    foreach ($line in $compilerEnvironment) {
        if ($line -match '^(INCLUDE|LIB|LIBPATH|PATH)=(.+)$') { $values[$Matches[1]] = $Matches[2] }
    }
    foreach ($key in @('INCLUDE', 'LIB', 'LIBPATH', 'PATH')) {
        if (-not $values[$key]) { throw "MSVC environment did not set $key." }
    }
    if ($env:GITHUB_ENV) {
        Add-Content -LiteralPath $env:GITHUB_ENV -Value "FFMPEG_MSYS2_ROOT=$msysRoot"
        foreach ($key in @('INCLUDE', 'LIB', 'LIBPATH')) {
            Add-Content -LiteralPath $env:GITHUB_ENV -Value "$key=$($values[$key])"
        }
    }
    if ($env:GITHUB_PATH) {
        $nativeTools = @($values['PATH'] -split ';' | Where-Object { $_ -and $_ -notin ($env:PATH -split ';') })
        [array]::Reverse($nativeTools)
        foreach ($directory in $nativeTools) { Add-Content -LiteralPath $env:GITHUB_PATH -Value $directory }
    }
}

$goSdkScript = Join-Path $repoRoot 'src/autolib-builder/go-sdk.cmake'
& $cmakeCommand -P $goSdkScript -- build -mod=readonly -trimpath -o (Join-Path $WorkRoot 'autolib-ci.exe') ./src/autolib-ci 2>&1 | ForEach-Object { [Console]::Error.WriteLine($_) }
if ($LASTEXITCODE -ne 0) { throw "Private Go SDK CI tool preparation failed: $LASTEXITCODE" }

if ($env:GITHUB_PATH) { Add-Content -LiteralPath $env:GITHUB_PATH -Value (Join-Path $cmakeDir 'bin') }
$result = [ordered]@{ cmake_command = $cmakeCommand; make_program = ''; budget = 0 }
$result | ConvertTo-Json -Compress
