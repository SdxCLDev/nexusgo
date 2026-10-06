# Ejecutar UNA VEZ como Administrador en el Windows Server, antes del primer
# arranque, para configurar las variables de entorno persistentes que Nexus
# exige fuera del ambiente "dev" (ver internal/config/config.go). Reemplazar
# los valores de ejemplo por los reales antes de ejecutar.
#
# setx escribe el valor en el registro de Windows (HKLM, por /M) para que
# quede disponible en NUEVAS sesiones/procesos. No afecta la sesion de
# PowerShell actual ni procesos ya corriendo -- despues de ejecutar este
# script hay que abrir una sesion nueva (o reiniciar) antes de levantar Nexus.

$ErrorActionPreference = "Stop"

setx NEXUS_ENV "prod" /M
setx NEXUS_JWT_SECRET "<reemplazar-por-un-secreto-largo-y-unico>" /M
setx NEXUS_SGP_API_KEY "<reemplazar-por-la-api-key-real-que-usara-sgp>" /M
setx NEXUS_DB_PATH "C:\Nexus\nexus.db" /M
setx NEXUS_HTTP_ADDR ":8080" /M

Write-Host ""
Write-Host "Variables de entorno configuradas a nivel de maquina."
Write-Host "Cerra esta sesion y abri una nueva (o reinicia el server) antes de ejecutar run.ps1,"
Write-Host "para que el proceso de Nexus las vea."
