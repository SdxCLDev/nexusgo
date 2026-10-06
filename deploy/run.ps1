# Levanta Nexus de forma simple: sin registrarlo como servicio de Windows
# (ver docs/10-plan-de-trabajo-poc.md -- se evaluara mas adelante si hace
# falta). Corre en primer plano y escribe el log tambien en nexus.log, junto
# al ejecutable.
#
# Antes de la primera ejecucion fuera de "dev", corre set-env.ps1 como
# Administrador (una sola vez) para configurar NEXUS_JWT_SECRET y
# NEXUS_SGP_API_KEY -- sin eso, Nexus no arranca (ver internal/config/config.go).
#
# Para dejarlo corriendo sin mantener la sesion de PowerShell abierta, se
# puede envolver este mismo script en una Tarea Programada ("Ejecutar tanto
# si el usuario inicio sesion como si no"), o simplemente iniciarlo con
# Start-Process en una ventana aparte.

$ErrorActionPreference = "Stop"
$scriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$exePath = Join-Path $scriptDir "nexus.exe"
$logPath = Join-Path $scriptDir "nexus.log"

if (-not (Test-Path $exePath)) {
    Write-Error "No se encontro $exePath -- copia nexus.exe junto a este script antes de ejecutarlo."
    exit 1
}

if (-not $env:NEXUS_ENV) {
    $env:NEXUS_ENV = "prod"
}

Write-Host "Iniciando Nexus (NEXUS_ENV=$($env:NEXUS_ENV), log en $logPath)..."
& $exePath *>> $logPath
