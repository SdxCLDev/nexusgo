# 6. Autenticación y Seguridad

Nexus maneja dos planos de seguridad distintos que no deben confundirse:

1. **Autenticación de entrada**: cómo los consumidores (SGP, otros sistemas internos) se autentican contra Nexus.
2. **Autenticación de salida**: cómo Nexus se autentica contra cada sistema externo (AMD, SAP, PEL).

El consumidor nunca ve ni maneja las credenciales de salida; esa complejidad queda completamente encapsulada dentro de Nexus.

## 6.1 Autenticación de entrada: API Key + JWT

Modelo elegido: cada sistema cliente de Nexus recibe una **API Key** de larga duración (gestionada de forma administrativa, fuera de banda), que usa para obtener **tokens JWT de corta duración** mediante el endpoint de autenticación. Las llamadas a la API funcional de Nexus se autentican con el JWT, no con la API Key directamente.

### 6.1.1 Flujo de obtención de token

```http
POST /api/v1/auth/token
Content-Type: application/json

{
  "client_id": "sgp",
  "api_key": "<api-key-secreta-de-sgp>"
}
```

```json
{
  "access_token": "eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9...",
  "token_type": "Bearer",
  "expires_in": 900
}
```

- `expires_in` en segundos (sugerido: 15 minutos).
- El cliente (ej. SGP) debe cachear el token y renovarlo antes de que expire, solicitando uno nuevo con la misma API Key.

### 6.1.2 Uso del token

```http
POST /api/v1/integrations/sgp-to-amd-envio-minuta/send
Authorization: Bearer eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9...
```

Todo endpoint funcional (excepto `/health`, `/ready` y `/auth/token`) exige este header.

### 6.1.3 Claims del JWT

```json
{
  "sub": "sgp",
  "scopes": ["integration:sgp-to-amd-envio-minuta:invoke", "integration:amd-to-sgp-descarga-minuta:invoke"],
  "iat": 1760000000,
  "exp": 1760000900,
  "iss": "nexus"
}
```

- `scopes` define explícitamente qué `integration_id` puede invocar ese cliente. Esto permite, por ejemplo, que un cliente distinto de SGP tenga acceso solo a un subconjunto de integraciones.
- Firmado con algoritmo asimétrico (ej. RS256), permitiendo que, si en el futuro se separan componentes, la validación del token no requiera compartir la clave de firma.

### 6.1.4 Autorización por `scope`

Antes de invocar una integración, el middleware de autenticación valida que el `scope` correspondiente a ese `integration_id` esté presente en el token. Si no, responde `403 FORBIDDEN`.

### 6.1.5 Gestión de API Keys

- Las API Keys se almacenan **hasheadas** (ej. bcrypt/argon2) en la base de datos de Nexus, nunca en texto plano.
- Cada API Key tiene asociado: `client_id`, lista de `scopes` permitidos, estado (`ACTIVE`/`REVOKED`), fecha de creación y último uso.
- Rotación: se debe poder emitir una nueva API Key para un cliente sin interrumpir servicio (período de solapamiento antes de revocar la anterior).
- Revocación inmediata: si una API Key se compromete, debe poder revocarse de forma administrativa, lo que invalida la emisión de nuevos JWT con ella (los JWT ya emitidos expiran naturalmente dentro de la ventana corta configurada).

## 6.2 Transporte

- Toda comunicación hacia Nexus es obligatoriamente **HTTPS/TLS**; no se expone HTTP plano en ningún ambiente salvo desarrollo local.
- Toda comunicación de Nexus hacia sistemas externos que soporten TLS debe usarlo; para SOAP, usar WS-Security/TLS según lo soporte el sistema externo.

## 6.3 Autenticación de salida: Nexus hacia sistemas externos

Cada integración (`internal/integrations/<sistema>`) encapsula su propio mecanismo de autenticación contra el sistema externo correspondiente, que puede variar: usuario/clave, token OAuth2, certificado de cliente, API key del sistema externo, etc.

Principios:

- La lógica de autenticación específica de cada sistema externo vive **únicamente** dentro del paquete de esa integración (ej. `internal/integrations/amd/client.go`), nunca en el núcleo.
- Los tokens/sesiones obtenidos se cachean en memoria con su tiempo de expiración, evitando autenticar en cada solicitud.
- Ante un rechazo de autenticación (401/403) del sistema externo, la integración reintenta una vez reautenticando antes de reportar error.

## 6.4 Almacenamiento de credenciales hacia sistemas externos

- Las credenciales de Nexus hacia AMD, SAP, PEL, etc. se almacenan **cifradas en reposo** en `internal/storage/credentialstore` (cifrado simétrico con clave gestionada fuera del binario, ej. variable de entorno inyectada por el orquestador de despliegue o un servicio de secretos si está disponible en la infraestructura de Sodexo).
- Nunca se registran credenciales ni tokens completos en logs (ver [§6.6 Redacción de datos sensibles](#66-redacción-de-datos-sensibles)).
- Se recomienda, a futuro, evaluar integración con un gestor de secretos centralizado (ej. HashiCorp Vault, Azure Key Vault) si el estándar de Sodexo lo contempla; en la primera fase, cifrado local + variable de entorno es aceptable.

## 6.5 Control de acceso por integración

- Cada integración declara explícitamente qué `scope` la protege (coincide con su `integration_id`).
- Esto permite, por ejemplo, otorgar a un cliente de monitoreo acceso de solo lectura al catálogo (`GET /integrations`) sin acceso de invocación a ninguna integración específica.

## 6.6 Redacción de datos sensibles

- El logging de requests/responses (tanto de entrada como hacia sistemas externos) debe **redactar** campos sensibles conocidos (contraseñas, tokens, números de documento, datos bancarios) antes de persistir o imprimir el log.
- Se mantiene una lista configurable de nombres de campo a redactar por integración, dado que cada sistema externo puede tener campos sensibles distintos.
- La auditoría de negocio (ver [Logging y Auditoría](07-logging-auditoria.md)) puede necesitar conservar el payload para trazabilidad, pero igualmente debe aplicar redacción a campos clasificados como sensibles.

## 6.7 Buenas prácticas adicionales

- **Rate limiting** por cliente (`client_id`) y, opcionalmente, por integración, para evitar que un consumidor sature a Nexus o, en cascada, a un sistema externo.
- **Principio de mínimo privilegio**: las credenciales de Nexus hacia la base de datos de SGP (en el modo `push_db`, ver [§5.5](05-patron-asincrono.md#55-modalidades-de-entrega-del-resultado-delivery_mode)) deben tener permisos acotados exclusivamente a las tablas/operaciones necesarias para esa integración.
- **Separación de ambientes**: credenciales, API Keys y configuración de endpoints externos deben ser distintas entre desarrollo, pruebas y producción, sin posibilidad de que una integración de pruebas alcance por error un sistema externo productivo.
