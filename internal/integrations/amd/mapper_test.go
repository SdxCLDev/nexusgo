package amd

import "testing"

func validDetalle() []MinutaDetalle {
	return []MinutaDetalle{
		{Ceco: "73110", IDReceta: 14110, CantidadComensales: 100, Fecha: "2026-06-07T00:00:00"},
	}
}

func TestValidate_OK(t *testing.T) {
	h := MinutaHeader{IDMinuta: 92032, Ceco: "73110"}
	if err := validate(h, validDetalle()); err != nil {
		t.Fatalf("se esperaba válida, error: %v", err)
	}
}

func TestValidate_Errors(t *testing.T) {
	cases := []struct {
		name    string
		header  MinutaHeader
		detalle []MinutaDetalle
	}{
		{"id_minuta inválido", MinutaHeader{IDMinuta: 0}, validDetalle()},
		{"sin detalle", MinutaHeader{IDMinuta: 1}, nil},
		{"id_receta inválido", MinutaHeader{IDMinuta: 1}, []MinutaDetalle{{IDReceta: 0, CantidadComensales: 1, Fecha: "2026-06-07"}}},
		{"comensales negativo", MinutaHeader{IDMinuta: 1}, []MinutaDetalle{{IDReceta: 1, CantidadComensales: -1, Fecha: "2026-06-07"}}},
		{"fecha vacía", MinutaHeader{IDMinuta: 1}, []MinutaDetalle{{IDReceta: 1, CantidadComensales: 1, Fecha: "  "}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := validate(tc.header, tc.detalle); err == nil {
				t.Errorf("se esperaba error de validación para %q", tc.name)
			}
		})
	}
}

func TestTransform_AgrupaEncabezadoYDetalle(t *testing.T) {
	h := MinutaHeader{IDMinuta: 92032, Ceco: "73110"}
	d := validDetalle()
	got := transform(h, d)
	if got.IDMinuta != 92032 {
		t.Errorf("IDMinuta = %d, se esperaba 92032", got.IDMinuta)
	}
	if got.Encabezado.Ceco != "73110" || len(got.Detalle) != 1 {
		t.Errorf("resultado inesperado: %+v", got)
	}
}
