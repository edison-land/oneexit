<#
.SYNOPSIS
    Build TempRoom for Windows.

.DESCRIPTION
    Produces dist\TempRoom-windows-<arch>\TempRoom.exe plus temproom-cli.exe,
    and dist\TempRoom-windows-<arch>.zip.

    Requires Go 1.26+. Google Chrome and the WebView2 runtime are needed at run
    time, not at build time (Windows 11 ships WebView2).

.PARAMETER Arch
    amd64 (default) or arm64.

.PARAMETER Console
    Keep the console subsystem so stdout/stderr stay visible. Useful when
    diagnosing a build; the default GUI build has no console window.

.EXAMPLE
    .\scripts\build-windows.ps1
.EXAMPLE
    .\scripts\build-windows.ps1 -Arch arm64 -Console
#>
param(
    [ValidateSet('amd64', 'arm64')][string]$Arch = 'amd64',
    [switch]$Console
)

$ErrorActionPreference = 'Stop'
if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
    throw 'go is not on PATH; install Go 1.26+ first'
}

$root = Split-Path -Parent $PSScriptRoot
Push-Location $root
try {
    $env:CGO_ENABLED = '0'
    $env:GOOS = 'windows'
    $env:GOARCH = $Arch

    # The desktop app embeds cmd/temproom-app/dist; frontend/dist is the editable source.
    Remove-Item -Recurse -Force cmd/temproom-app/dist -ErrorAction SilentlyContinue
    Copy-Item -Recurse frontend/dist cmd/temproom-app/dist

    $buildArgs = @('-trimpath')
    $ldflags = @()
    if (-not $Console) { $ldflags += '-H=windowsgui' }
    if ($env:TEMPROOM_DEFAULT_RELAY) {
        $ldflags += "-X temproom/internal/app.DefaultServerURL=$env:TEMPROOM_DEFAULT_RELAY"
    }
    if ($ldflags.Count -gt 0) { $buildArgs += @('-ldflags', ($ldflags -join ' ')) }

    $out = "dist\TempRoom-windows-$Arch"
    Remove-Item -Recurse -Force $out -ErrorAction SilentlyContinue
    New-Item -ItemType Directory -Force -Path $out | Out-Null

    go test -count=1 ./...
    go build @buildArgs -o "$out\TempRoom.exe" ./cmd/temproom-app
    go build -trimpath -o "$out\temproom-cli.exe" ./cmd/temproom-cli
    # Local relay for self-testing without a Cloudflare Worker (PORT, default 8080).
    go build -trimpath -o "$out\temproom-server.exe" ./cmd/temproom-server

    Compress-Archive -Path "$out\*" -DestinationPath "dist\TempRoom-windows-$Arch.zip" -Force
    Write-Host "built: $out and dist\TempRoom-windows-$Arch.zip"
}
finally {
    Pop-Location
}
