# Kryton guest agent. Installed by the OEM install.bat Kryton generates for
# machines created with dockur.jobAgent=true; started from the all-users
# Startup folder at every logon (dockur autologs in by default).
#
# Protocol (shared folder, see internal/dockur/jobs.go): for every job
# directory under <share>\.kryton\jobs that has job.json and no status.json,
# run the script, write stdout.log, status.json and artifacts\.
$ErrorActionPreference = 'Continue'

function Get-KrytonRoot {
    foreach ($c in @('\\host.lan\Data', 'Z:\')) {
        $r = Join-Path $c '.kryton'
        if (Test-Path -LiteralPath $r) { return $r }
    }
    return $null
}

function Write-Json($path, $obj) {
    $tmp = "$path.tmp"
    ($obj | ConvertTo-Json -Compress) | Set-Content -LiteralPath $tmp -Encoding ASCII
    Move-Item -LiteralPath $tmp -Destination $path -Force
}

function Send-Beat($root) {
    try {
        Set-Content -LiteralPath (Join-Path $root 'agent-ready') -Value (Get-Date).ToUniversalTime().ToString('o') -Encoding ASCII
    } catch { }
}

function Stop-Tree($proc) {
    & taskkill.exe /T /F /PID $proc.Id 2>&1 | Out-Null
}

function Invoke-KrytonJob($root, $dir) {
    $status = Join-Path $dir 'status.json'
    $cancel = Join-Path $dir 'cancel'
    try {
        $req = Get-Content -LiteralPath (Join-Path $dir 'job.json') -Raw | ConvertFrom-Json
    } catch {
        Write-Json $status @{ state = 'failed' }
        return
    }
    if (Test-Path -LiteralPath $cancel) { Write-Json $status @{ state = 'canceled' }; return }

    $started = (Get-Date).ToUniversalTime().ToString('o')
    Write-Json $status @{ state = 'running'; startedAt = $started }

    $art = Join-Path $dir 'artifacts'
    New-Item -ItemType Directory -Force -Path $art | Out-Null
    $out = Join-Path $dir 'stdout.log'
    $script = Join-Path $dir ([string]$req.scriptFile)

    $envNames = @()
    $env:KRYTON_JOB_ID = [string]$req.id
    $env:KRYTON_ARTIFACTS = $art
    $env:KRYTON_SHARED = Split-Path -Parent $root
    $envNames += @('KRYTON_JOB_ID', 'KRYTON_ARTIFACTS', 'KRYTON_SHARED')
    if ($req.env) {
        foreach ($p in $req.env.PSObject.Properties) {
            Set-Item -Path ("Env:" + $p.Name) -Value ([string]$p.Value)
            $envNames += $p.Name
        }
    }

    if ($req.shell -eq 'powershell') {
        $inner = '"powershell.exe" -NoProfile -ExecutionPolicy Bypass -File "' + $script + '"'
    } else {
        $inner = 'call "' + $script + '"'
    }
    $argLine = '/d /s /c "' + $inner + ' > "' + $out + '" 2>&1"'
    $proc = Start-Process -FilePath 'cmd.exe' -ArgumentList $argLine -PassThru -WindowStyle Hidden
    $null = $proc.Handle

    $deadline = (Get-Date).AddSeconds([int]$req.timeoutSeconds)
    $final = $null
    while (-not $proc.HasExited) {
        Send-Beat $root
        if (Test-Path -LiteralPath $cancel) { Stop-Tree $proc; $final = 'canceled'; break }
        if ((Get-Date) -gt $deadline) { Stop-Tree $proc; $final = 'timed_out'; break }
        Start-Sleep -Milliseconds 1000
    }
    $code = $null
    if (-not $final) {
        $proc.WaitForExit()
        $code = $proc.ExitCode
        $final = 'done'
    }
    foreach ($n in $envNames) { Remove-Item -Path ("Env:" + $n) -ErrorAction SilentlyContinue }
    Write-Json $status @{ state = $final; exitCode = $code; startedAt = $started; finishedAt = (Get-Date).ToUniversalTime().ToString('o') }
}

while ($true) {
    $root = Get-KrytonRoot
    if ($root) {
        Send-Beat $root
        $jobs = Join-Path $root 'jobs'
        if (Test-Path -LiteralPath $jobs) {
            foreach ($d in Get-ChildItem -LiteralPath $jobs -Directory -ErrorAction SilentlyContinue) {
                if ((Test-Path -LiteralPath (Join-Path $d.FullName 'job.json')) -and -not (Test-Path -LiteralPath (Join-Path $d.FullName 'status.json'))) {
                    Invoke-KrytonJob $root $d.FullName
                }
            }
        }
    }
    Start-Sleep -Seconds 2
}
