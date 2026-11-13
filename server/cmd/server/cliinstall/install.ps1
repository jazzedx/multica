# Installs the CLI from a self-hosted Multica server: the one built alongside
# that server.
#
# Usage:
#   $env:MULTICA_SERVER_URL="<server>"; irm <server>/api/cli/install.ps1 | iex
#
# Environment:
#   MULTICA_SERVER_URL   server URL (required)
#   MULTICA_INSTALL_DIR  install directory, %USERPROFILE%\.multica\bin by default

$ErrorActionPreference = "Stop"

function Install-MulticaCli {
    $serverUrl = "$env:MULTICA_SERVER_URL".TrimEnd("/")
    if (-not $serverUrl) {
        throw 'multica install: missing server URL. Usage: $env:MULTICA_SERVER_URL="<server>"; irm <server>/api/cli/install.ps1 | iex'
    }

    $arch = switch ($env:PROCESSOR_ARCHITECTURE) {
        "AMD64" { "amd64" }
        "ARM64" { "arm64" }
        default { throw "multica install: unsupported architecture $env:PROCESSOR_ARCHITECTURE" }
    }

    $installDir = if ($env:MULTICA_INSTALL_DIR) { $env:MULTICA_INSTALL_DIR } else { Join-Path $env:USERPROFILE ".multica\bin" }
    $tmp = Join-Path ([System.IO.Path]::GetTempPath()) ("multica-install-" + [System.Guid]::NewGuid().ToString("N"))
    New-Item -ItemType Directory -Path $tmp | Out-Null

    try {
        $zip = Join-Path $tmp "multica.zip"
        $url = "$serverUrl/api/cli/download/windows/$arch"
        Write-Host "Downloading multica (windows/$arch) from $serverUrl"
        $response = Invoke-WebRequest -Uri $url -OutFile $zip -PassThru -UseBasicParsing

        # Windows PowerShell 5.1 returns a string here and PowerShell 7 an array
        # of strings; interpolation makes both a string.
        $expected = "$($response.Headers['X-Checksum-Sha256'])".Trim().ToLowerInvariant()
        $version = "$($response.Headers['X-Multica-Version'])".Trim()
        if (-not $expected) {
            throw "multica install: the server did not send a checksum for $url"
        }
        $actual = (Get-FileHash -Path $zip -Algorithm SHA256).Hash.ToLowerInvariant()
        if ($actual -ne $expected) {
            throw "multica install: checksum mismatch: expected $expected, got $actual"
        }

        Expand-Archive -Path $zip -DestinationPath $tmp -Force
        New-Item -ItemType Directory -Path $installDir -Force | Out-Null
        $target = Join-Path $installDir "multica.exe"
        Move-Item -Path (Join-Path $tmp "multica.exe") -Destination $target -Force
        if (-not $version) { $version = "(unknown version)" }
        Write-Host "Installed multica $version to $target"

        $userPath = [Environment]::GetEnvironmentVariable("Path", "User")
        if (($userPath -split ";") -notcontains $installDir) {
            [Environment]::SetEnvironmentVariable("Path", "$installDir;$userPath", "User")
            $env:Path = "$installDir;$env:Path"
            Write-Host "Added $installDir to your user PATH. Open a new terminal for other windows to pick it up."
        }
    }
    finally {
        Remove-Item -Path $tmp -Recurse -Force -ErrorAction SilentlyContinue
    }
}

Install-MulticaCli
