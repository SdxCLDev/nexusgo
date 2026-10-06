# Levanta Nexus de forma simple: sin registrarlo como servicio de Windows
# (ver docs/10-plan-de-trabajo-poc.md -- se evaluara mas adelante si hace
# falta). Corre en primer plano y escribe el log tambien en nexus.log, junto
# al ejecutable.
#
# Si existe "env.local.ps1" junto a este script, lo carga automaticamente
# (copia env.local.ps1.example, completa los valores reales y guardalo sin
# el ".example"). Esto fija las variables de entorno solo para este proceso
# y el de nexus.exe que lanza -- no toca el registro de Windows ni requiere
# ejecutar como Administrador.
#
# Fuera del ambiente "dev", NEXUS_JWT_SECRET y NEXUS_SGP_API_KEY son
# obligatorias -- sin ellas Nexus no arranca (ver internal/config/config.go).
#
# Para dejarlo corriendo sin mantener la sesion de PowerShell abierta, se
# puede envolver este mismo script en una Tarea Programada ("Ejecutar tanto
# si el usuario inicio sesion como si no"), o simplemente iniciarlo con
# Start-Process en una ventana aparte.

$ErrorActionPreference = "Stop"
$scriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$exePath = Join-Path $scriptDir "nexus.exe"
$logPath = Join-Path $scriptDir "nexus.log"
$envLocalPath = Join-Path $scriptDir "env.local.ps1"

if (-not (Test-Path $exePath)) {
    Write-Error "No se encontro $exePath -- copia nexus.exe junto a este script antes de ejecutarlo."
    exit 1
}

if (Test-Path $envLocalPath) {
    Write-Host "Cargando variables de entorno desde env.local.ps1..."
    . $envLocalPath
} else {
    Write-Host "No se encontro env.local.ps1 (ver env.local.ps1.example) -- se usaran las variables de entorno ya configuradas, si existen."
}

if (-not $env:NEXUS_ENV) {
    $env:NEXUS_ENV = "prod"
}

Write-Host "Iniciando Nexus (NEXUS_ENV=$($env:NEXUS_ENV), log en $logPath)..."
& $exePath *>> $logPath
