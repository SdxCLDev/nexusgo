package amd

import (
	"fmt"
	"strings"
)

// MinutaHeader refleja un encabezado devuelto por el stored procedure
// amd_get_minutasintegra. Los nombres de campo replican los de AMD (incluidas
// sus mayúsculas); el mapeo al modelo de SGP se hace en transform.
type MinutaHeader struct {
	IDContrato             int     `json:"id_contrato"`
	IDMinuta               int     `json:"id_minuta"`
	FechaMinuta            string  `json:"fechaMinuta"`
	Ceco                   string  `json:"ceco"`
	NombreCeco             string  `json:"nombreCeco"`
	CecoLider              string  `json:"cecoLider"`
	Regimen                string  `json:"regimen"`
	SerNombre              string  `json:"ser_nombre"`
	CostoBandejaResultante float64 `json:"Costo_bandeja_resultante"`
	CostoBandejaTarget     float64 `json:"Costo_bandeja_target"`
	Segment                string  `json:"segment"`
	Plantilla              int     `json:"plantilla"`
	IDRegimen              int     `json:"id_regimen"`
	IDTipoServicio         int     `json:"id_tipo_servicio"`
}

// MinutaDetalle refleja una fila del detalle devuelto por el stored procedure
// amd_getMinutaIntegracion.
//
// Los campos numéricos son float64 (no int) a propósito: AMD serializa varios
// valores con decimales (ej. "ponderaciones": 100.00, "cantidad_comensales":
// 50.00). float64 acepta tanto enteros (100) como decimales (100.00) sin
// fallar al deserializar, y al re-serializar un valor entero (100.0) Go lo
// emite como "100", de modo que la salida para SGP queda limpia.
type MinutaDetalle struct {
	Ceco               string  `json:"ceco"`
	Regimen            float64 `json:"regimen"`
	Servicio           float64 `json:"servicio"`
	Fecha              string  `json:"fecha"`
	MacroEstructura    float64 `json:"macroEstructura"`
	IDReceta           float64 `json:"id_receta"`
	CantidadComensales float64 `json:"cantidad_comensales"`
	Ponderaciones      float64 `json:"ponderaciones"`
	TipoPlato          float64 `json:"tipo_plato"`
	Estructura         float64 `json:"estructura"`
	Orden              float64 `json:"orden"`
}

// MinutaResult es una minuta ya descargada, validada y transformada: la unidad
// que SGP revisa vía GET /jobs/{id}/result (delivery_mode pull_api) y que, en
// una fase posterior, se insertará en la base de datos de SGP (push_db). Se
// persiste como el `data` de cada job_item — ver docs/08-modelo-datos.md §8.3.1.
type MinutaResult struct {
	IDMinuta   int             `json:"id_minuta"`
	Encabezado MinutaHeader    `json:"encabezado"`
	Detalle    []MinutaDetalle `json:"detalle"`
}

// transform arma el resultado de negocio a partir del encabezado y su detalle.
// Hoy agrupa ambos sin reestructurar; cuando se defina el modelo exacto de SGP
// (push_db, Fase 7) este es el punto donde se adapta nombre por nombre.
func transform(h MinutaHeader, detalle []MinutaDetalle) MinutaResult {
	return MinutaResult{
		IDMinuta:   h.IDMinuta,
		Encabezado: h,
		Detalle:    detalle,
	}
}

// validate aplica reglas mínimas de consistencia a una minuta descargada. Son
// validaciones estructurales; las reglas de negocio reales de SGP se afinarán
// cuando se definan (ver docs/10-plan-de-trabajo-poc.md Fase 7). Una minuta que
// no pasa validación se marca como ítem FAILED (resultado PARTIAL del job), sin
// abortar el resto de la descarga — ver docs/05-patron-asincrono.md §5.9.
func validate(h MinutaHeader, detalle []MinutaDetalle) error {
	if h.IDMinuta <= 0 {
		return fmt.Errorf("id_minuta inválido (%d)", h.IDMinuta)
	}
	if len(detalle) == 0 {
		return fmt.Errorf("la minuta %d no tiene filas de detalle", h.IDMinuta)
	}
	for idx, d := range detalle {
		if d.IDReceta <= 0 {
			return fmt.Errorf("detalle #%d: id_receta inválido (%v)", idx, d.IDReceta)
		}
		if d.CantidadComensales < 0 {
			return fmt.Errorf("detalle #%d: cantidad_comensales negativa (%v)", idx, d.CantidadComensales)
		}
		if strings.TrimSpace(d.Fecha) == "" {
			return fmt.Errorf("detalle #%d: fecha vacía", idx)
		}
	}
	return nil
}
