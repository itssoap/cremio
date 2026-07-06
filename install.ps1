<#
.SYNOPSIS
    Cremio Windows installer and updater
.DESCRIPTION
    Downloads a cremio release from GitHub, installs or updates it, and adds it
    to your user PATH. Re-running the script checks for updates. Update detection
    compares the SHA-256 of your installed binary against the SHA-256 published
    for the release asset (falling back to version tags if no digest is exposed).

    Replacement targets are detected in this order:
      1. .\cremio.exe in the current working directory
      2. cremio.exe found on your PATH
      3. Fresh install to %LOCALAPPDATA%\cremio\bin
.PARAMETER InstallDir
    Directory for a fresh install. Defaults to $env:LOCALAPPDATA\cremio\bin.
.PARAMETER PreRelease
    Install or update to the newest pre-release build.
.PARAMETER CheckOnly
    Only report whether a new version is available. Makes no changes.
.PARAMETER NoPath
    Skip adding the install directory to PATH.
.EXAMPLE
    irm https://raw.githubusercontent.com/itssoap/cremio/main/install.ps1 | iex
.EXAMPLE
    # Pass flags when piping from the web:
    & ([scriptblock]::Create((irm https://raw.githubusercontent.com/itssoap/cremio/main/install.ps1))) -CheckOnly
.NOTES
    One-liner: irm https://raw.githubusercontent.com/itssoap/cremio/main/install.ps1 | iex
#>

[CmdletBinding()]
param(
    [string]$InstallDir = "$env:LOCALAPPDATA\cremio\bin",
    [switch]$PreRelease,
    [switch]$CheckOnly,
    [switch]$NoPath
)

function Install-Cremio {
    [CmdletBinding()]
    param(
        [string]$InstallDir = "$env:LOCALAPPDATA\cremio\bin",
        [switch]$PreRelease,
        [switch]$CheckOnly,
        [switch]$NoPath
    )

    $ErrorActionPreference = "Stop"
    $ProgressPreference    = "SilentlyContinue"

    $Repo        = "itssoap/cremio"
    $Binary      = "cremio.exe"
    $VersionFile = Join-Path (Split-Path $InstallDir -Parent) ".version"

    # --- helpers ---------------------------------------------------------
    function Write-Step($msg) { Write-Host ":: " -NoNewline -ForegroundColor Cyan; Write-Host $msg }
    function Write-Info($msg) { Write-Host "   $msg" -ForegroundColor Gray }
    function Write-Warn($msg) { Write-Host "!! " -NoNewline -ForegroundColor Yellow; Write-Host $msg }

    function Get-ArchSuffix {
        $arch = switch ([System.Runtime.InteropServices.RuntimeInformation]::ProcessArchitecture) {
            "Arm64" { "arm64" }
            "X86"   { "386"   }
            default { "amd64" }
        }
        return "windows-$arch"
    }

    function Invoke-GitHubApi($url) {
        for ($attempt = 1; $attempt -le 2; $attempt++) {
            try {
                return Invoke-RestMethod -Uri $url -Method Get -ErrorAction Stop
            } catch {
                $status = $null
                try { $status = [int]$_.Exception.Response.StatusCode } catch {}
                if ($status -in @(403, 429)) {
                    $resetEpoch = $null
                    try { $resetEpoch = $_.Exception.Response.Headers.GetValues('X-RateLimit-Reset')[0] } catch {}
                    if ($resetEpoch) {
                        $resetAt = [DateTimeOffset]::FromUnixTimeSeconds([long]$resetEpoch).LocalDateTime.ToString('HH:mm')
                        throw "GitHub API rate limit exceeded. Limit resets at $resetAt -- try again then, OR fetch it from https://github.com/$Repo/releases."
                    }
                    throw "GitHub API rate limit exceeded. Wait a few minutes and try again OR fetch it from https://github.com/$Repo/releases."
                }
                if ($attempt -lt 2) { Write-Info "GitHub API call failed, retrying..." }
                else { throw "Could not reach GitHub API. Check your internet connection and try again.`n$($_.Exception.Message)" }
            }
        }
    }

    function Get-ReleaseData {
        if ($PreRelease) {
            $list = Invoke-GitHubApi "https://api.github.com/repos/$Repo/releases"
            if (-not $list -or $list.Count -eq 0) { throw "No releases found for $Repo." }
            # /releases is sorted newest-first and includes pre-releases
            return $list[0]
        }
        return Invoke-GitHubApi "https://api.github.com/repos/$Repo/releases/latest"
    }

    # --- main ------------------------------------------------------------
    Write-Host ""
    Write-Host "  Cremio Windows Installer" -ForegroundColor Green
    Write-Info "https://github.com/$Repo"
    if ($PreRelease) { Write-Info "channel: pre-release" }
    if ($CheckOnly)  { Write-Info "mode: check only (no changes will be made)" }
    Write-Host ""

    # 1. Fetch release info
    Write-Step "Fetching release info..."
    $release = Get-ReleaseData
    $tag     = $release.tag_name
    Write-Info "Release: $tag"

    # 2. Pick the right asset for this architecture
    $archSuffix   = Get-ArchSuffix
    $assetPattern = "*$archSuffix.exe"
    $asset = $release.assets | Where-Object { $_.name -like $assetPattern } | Select-Object -First 1

    if (-not $asset -and $archSuffix -eq "windows-arm64") {
        Write-Info "No native arm64 asset found, falling back to amd64 (runs via x64 emulation)"
        $archSuffix   = "windows-amd64"
        $assetPattern = "*$archSuffix.exe"
        $asset = $release.assets | Where-Object { $_.name -like $assetPattern } | Select-Object -First 1
    }
    if (-not $asset) {
        throw "No release asset found matching '$archSuffix'. Available assets: $($release.assets.name -join ', ')"
    }

    # GitHub exposes a per-asset digest like "sha256:abc..." when available
    $assetDigest = $null
    if ($asset.PSObject.Properties.Name -contains 'digest' -and $asset.digest) {
        $assetDigest = ($asset.digest -replace '^sha256:', '').ToLower()
    }

    # 3. Detect replacement target: (a) .\cremio.exe in CWD, then (b) PATH
    $targetBin = $null
    $cwdBin = Join-Path (Get-Location).Path $Binary
    if (Test-Path $cwdBin) {
        $targetBin = $cwdBin
        Write-Info "Found local binary in CWD: $targetBin"
    } else {
        $existingCmd = Get-Command $Binary -ErrorAction SilentlyContinue
        if ($existingCmd) {
            $targetBin = $existingCmd.Source
            Write-Info "Found binary on PATH: $targetBin"
        }
    }

    # 4. Decide whether an update is needed (SHA-256 first, tag fallback)
    $upToDate  = $false
    $localSha  = $null
    if ($targetBin) {
        if ($assetDigest) {
            try { $localSha = (Get-FileHash -Path $targetBin -Algorithm SHA256).Hash.ToLower() } catch {}
            if ($localSha) {
                Write-Info "Installed SHA-256: $localSha"
                Write-Info "Release   SHA-256: $assetDigest"
                if ($localSha -eq $assetDigest) { $upToDate = $true }
            }
        }
        if (-not $assetDigest -or -not $localSha) {
            # Tag fallback: embedded ProductVersion, then .version file
            $existingVersion = $null
            try {
                $vi = (Get-Item $targetBin -ErrorAction Stop).VersionInfo
                if ($vi.ProductVersion -and $vi.ProductVersion -notmatch '^0\.0') {
                    $existingVersion = $vi.ProductVersion.TrimStart('v')
                }
            } catch {}
            if (-not $existingVersion -and (Test-Path $VersionFile)) {
                $existingVersion = (Get-Content $VersionFile -Raw).Trim().TrimStart('v')
            }
            if ($existingVersion -and $existingVersion -eq $tag.TrimStart('v')) { $upToDate = $true }
        }
    }

    if ($upToDate) {
        Write-Host ""
        Write-Host "  Cremio $tag is already up to date." -ForegroundColor DarkGreen
        Write-Info $targetBin
        Write-Host ""
        return
    }

    if ($targetBin) {
        if ($CheckOnly) {
            Write-Host ""
            Write-Host "  Update available: $tag" -ForegroundColor Yellow
            Write-Info "Installed binary: $targetBin"
            Write-Info "Run without -CheckOnly to install it."
            Write-Host ""
            return
        }
        Write-Step "Updating cremio -> $tag"
    } else {
        if ($CheckOnly) {
            Write-Host ""
            Write-Host "  Cremio is not installed. Latest available: $tag" -ForegroundColor Yellow
            Write-Info "Run without -CheckOnly to install it."
            Write-Host ""
            return
        }
        Write-Step "Installing cremio $tag..."
    }

    # 5. Choose destination: replace target in place when writable, else InstallDir
    $dest            = Join-Path $InstallDir $Binary
    $replacedInPlace = $false
    if ($targetBin) {
        $targetDir = Split-Path $targetBin -Parent
        $testFile  = Join-Path $targetDir ".cremio_write_test_$(Get-Random)"
        try {
            [System.IO.File]::WriteAllText($testFile, "")
            Remove-Item $testFile -Force -ErrorAction SilentlyContinue
            $dest            = $targetBin
            $replacedInPlace = $true
            Write-Info "Replacing in-place: $dest"
        } catch {
            Write-Info "Cannot write to $targetDir -- installing to $InstallDir instead"
        }
    }

    if (-not (Test-Path (Split-Path $dest -Parent))) {
        New-Item -ItemType Directory -Path (Split-Path $dest -Parent) -Force | Out-Null
    }

    # 6. Download to temp, verify digest, then move into place
    Write-Info "Downloading $($asset.name) ..."
    $tmpFile = Join-Path $env:TEMP "cremio_$([Guid]::NewGuid().ToString('N')).exe"
    try {
        Invoke-WebRequest -Uri $asset.browser_download_url -OutFile $tmpFile -ErrorAction Stop

        if ($assetDigest) {
            $dlSha = (Get-FileHash -Path $tmpFile -Algorithm SHA256).Hash.ToLower()
            if ($dlSha -ne $assetDigest) {
                throw "SHA-256 mismatch on downloaded file. Expected $assetDigest, got $dlSha. Aborting."
            }
            Write-Info "Download verified (sha256 ok)"
        }

        Move-Item -Path $tmpFile -Destination $dest -Force -ErrorAction Stop
    } finally {
        if (Test-Path $tmpFile) { Remove-Item $tmpFile -Force -ErrorAction SilentlyContinue }
    }
    Write-Info "Installed to $dest"

    # 7. Record version
    $tag | Set-Content -Path $VersionFile -Force -NoNewline

    # 8. Add to PATH (skip on in-place replace or -NoPath)
    if (-not $NoPath -and -not $replacedInPlace) {
        Write-Step "Checking PATH..."
        $installedDir = Split-Path $dest -Parent
        $currentUserPath = [Environment]::GetEnvironmentVariable("Path", "User")
        $pathEntries = if ($currentUserPath) { $currentUserPath -split ';' } else { @() }
        if ($pathEntries -notcontains $installedDir) {
            Write-Info "Adding $installedDir to user PATH"
            $newPath = "$currentUserPath;$installedDir".TrimStart(';')
            try {
                [Environment]::SetEnvironmentVariable("Path", $newPath, "User")
            } catch [System.Security.SecurityException] {
                throw "Could not update PATH environment variable: $($_.Exception.Message)"
            }
            $env:Path = "$env:Path;$installedDir"
            Write-Info "PATH updated for this session and future terminals."
        } else {
            Write-Info "$installedDir is already on user PATH."
        }
    }

    # 9. Verify
    Write-Step "Verifying installation..."
    if (Test-Path $dest) {
        $size = (Get-Item $dest).Length
        Write-Info "cremio.exe is present ($([math]::Round($size/1MB, 1)) MB)"
    } else {
        throw "Installation verification failed: $dest not found"
    }

    Write-Host ""
    Write-Host "  Cremio $tag is ready!" -ForegroundColor Green
    Write-Host "  Run 'cremio' in a new terminal to get started." -ForegroundColor Cyan
    Write-Host "  Re-run with -CheckOnly anytime to look for updates." -ForegroundColor DarkGray
    Write-Host ""
}

try {
    Install-Cremio @PSBoundParameters
} catch {
    Write-Host ""
    Write-Host "  ERROR: $($_.Exception.Message)" -ForegroundColor Red
    Write-Host ""
    exit 1
}
