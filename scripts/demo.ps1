$ErrorActionPreference = 'Stop'

$ComposeFile = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..\docker-compose.yml'))
$GoUrl = if ($env:GAMESTATS_GO_URL) { $env:GAMESTATS_GO_URL } else { 'http://localhost:8080' }
$ScalaUrl = if ($env:GAMESTATS_SCALA_URL) { $env:GAMESTATS_SCALA_URL } else { 'http://localhost:8081' }
$TimeoutSeconds = 90
$TimeoutWatch = [System.Diagnostics.Stopwatch]::StartNew()

function Test-Timeout {
    return $TimeoutWatch.Elapsed.TotalSeconds -ge $TimeoutSeconds
}

function Wait-ForUrl([string]$Url) {
    while (-not (Test-Timeout)) {
        & curl.exe --fail --silent --output NUL --max-time 2 $Url 2>$null
        if ($LASTEXITCODE -eq 0) {
            return
        }
        Start-Sleep -Seconds 1
    }
    throw "Timeout de $TimeoutSeconds s esperando $Url. Comprueba Docker Desktop y Compose."
}

function Wait-ForSimulator {
    Write-Host 'Esperando a que termine el contenedor simulator...'
    while (-not (Test-Timeout)) {
        $ContainerId = @(& docker compose -f $ComposeFile ps --all --quiet simulator 2>$null | Select-Object -First 1)[0]
        if (-not [string]::IsNullOrWhiteSpace($ContainerId)) {
            $ContainerId = $ContainerId.Trim()
            $State = & docker inspect --format '{{.State.Status}} {{.State.ExitCode}}' $ContainerId 2>$null
            if ($State -eq 'exited 0') {
                return
            }
            if ($State -match '^exited ' -and $State -ne 'exited 0') {
                throw "El contenedor simulator termino con estado: $State."
            }
        }
        Start-Sleep -Seconds 1
    }
    throw "Timeout de $TimeoutSeconds s esperando que termine simulator."
}

function Get-LeaderboardMatchCount([string]$GameId) {
    $Body = & curl.exe --fail --silent --max-time 3 "$ScalaUrl/v1/games/$GameId/leaderboard" 2>$null
    if ($LASTEXITCODE -ne 0) {
        return $null
    }
    try {
        $Entries = @(ConvertFrom-Json -InputObject ($Body -join "`n"))
    } catch {
        return $null
    }
    $Count = 0
    foreach ($Entry in $Entries) {
        foreach ($MatchValue in @($Entry.matches)) {
            $Count += [int]$MatchValue
        }
    }
    return $Count
}

function Get-TotalMatchCount {
    $CombatCount = Get-LeaderboardMatchCount 'combat-arena'
    $RacingCount = Get-LeaderboardMatchCount 'racing-circuit'
    if ($null -eq $CombatCount -or $null -eq $RacingCount) {
        return $null
    }
    return [int]($CombatCount + $RacingCount)
}

function Wait-ForStableAnalysis {
    Write-Host ("Esperando a que termine el an{0}lisis..." -f [char]0x00E1)
    $PreviousCount = $null
    $StableWatch = $null
    while (-not (Test-Timeout)) {
        $CurrentCount = Get-TotalMatchCount
        if ($null -eq $CurrentCount -or $CurrentCount -le 0) {
            $PreviousCount = $null
            $StableWatch = $null
        } elseif ($null -eq $PreviousCount -or $CurrentCount -ne $PreviousCount) {
            $PreviousCount = $CurrentCount
            $StableWatch = [System.Diagnostics.Stopwatch]::StartNew()
        } elseif ($StableWatch.Elapsed.TotalSeconds -ge 10) {
            return
        }
        Start-Sleep -Seconds 1
    }
    throw "Timeout de $TimeoutSeconds s: el total de partidas no se mantuvo estable durante 10 s."
}

Write-Host "Esperando Go en $GoUrl/health..."
Wait-ForUrl "$GoUrl/health"
Wait-ForSimulator
Wait-ForStableAnalysis

function Invoke-Demo([string]$Title, [string]$Url) {
    Write-Host "`n=== $Title ==="
    & curl.exe --fail --silent --show-error $Url
    if ($LASTEXITCODE -ne 0) {
        throw "Fallo la consulta: $Url"
    }
    Write-Host ''
}

Invoke-Demo 'Health (Go)' "$GoUrl/health"
Invoke-Demo 'Leaderboard: combat-arena' "$ScalaUrl/v1/games/combat-arena/leaderboard"
Invoke-Demo 'Leaderboard: racing-circuit' "$ScalaUrl/v1/games/racing-circuit/leaderboard"
Invoke-Demo 'Patrones: combat-arena' "$ScalaUrl/v1/patterns?gameId=combat-arena"
Invoke-Demo 'Patrones: racing-circuit' "$ScalaUrl/v1/patterns?gameId=racing-circuit"
Invoke-Demo 'Estadisticas: p-01' "$ScalaUrl/v1/stats/players/p-01"
Invoke-Demo 'Partida: m-pat-c-0001' "$ScalaUrl/v1/matches/m-pat-c-0001?gameId=combat-arena"