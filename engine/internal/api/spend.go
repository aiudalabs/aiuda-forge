package api

// Spend desde GitHub (F4 del pivot github-native): expone el gasto medido del
// repo del proyecto (Copilot premium requests, Actions minutes, LFS, …) del ciclo
// actual, leído de la plataforma de facturación mejorada de GitHub. Si GitHub no
// expone la facturación para el owner del repo (cuenta personal, o token sin
// permisos de billing de la org), degrada a {available:false} con 200 — no es una
// falla del sistema.

import (
	"errors"
	"net/http"
	"time"

	"forge/internal/github"
	"forge/internal/projects"
)

// spendItem es una fila agregada por SKU del ciclo actual (montos en USD).
type spendItem struct {
	Product     string  `json:"product"`
	SKU         string  `json:"sku"`
	Quantity    float64 `json:"quantity"`
	UnitType    string  `json:"unit_type"`
	GrossAmount float64 `json:"gross_amount"`
	NetAmount   float64 `json:"net_amount"`
}

// githubSpend maneja GET /projects/{id}/spend/github (miembro+).
func (s *Server) githubSpend(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.requireRole(r.Context(), id, projects.RoleViewer) {
		httpErr(w, http.StatusForbidden, "viewing spend requires project membership")
		return
	}
	p, err := s.Projects.Get(id)
	if err != nil {
		if errors.Is(err, projects.ErrNotFound) {
			httpErr(w, http.StatusNotFound, "project not found: "+id)
			return
		}
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if p.Repo == "" {
		writeJSON(w, http.StatusOK, map[string]any{
			"available": false,
			"reason":    "el proyecto no tiene un repositorio en GitHub",
		})
		return
	}

	now := time.Now().UTC()
	items, err := github.New().BillingUsage(r.Context(), p.Repo, now.Year(), int(now.Month()))
	if err != nil {
		if errors.Is(err, github.ErrBillingUnavailable) {
			writeJSON(w, http.StatusOK, map[string]any{
				"available": false,
				"reason":    "GitHub no expone el gasto de facturación para este repo (cuenta personal, o el token no tiene permisos de facturación de la organización)",
			})
			return
		}
		httpErr(w, http.StatusBadGateway, "github: "+err.Error())
		return
	}

	// Agrega por (producto, sku, unidad): el reporte del ciclo trae filas diarias
	// que hay que colapsar. Totales por producto = neto (lo que realmente se debe).
	type aggKey struct{ product, sku, unit string }
	agg := map[aggKey]*spendItem{}
	var order []aggKey
	totals := map[string]float64{}
	for _, it := range items {
		k := aggKey{it.Product, it.SKU, it.UnitType}
		row := agg[k]
		if row == nil {
			row = &spendItem{Product: it.Product, SKU: it.SKU, UnitType: it.UnitType}
			agg[k] = row
			order = append(order, k)
		}
		row.Quantity += it.Quantity
		row.GrossAmount += it.GrossAmount
		row.NetAmount += it.NetAmount
		totals[it.Product] += it.NetAmount
	}
	out := make([]spendItem, 0, len(order))
	for _, k := range order {
		out = append(out, *agg[k])
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"available": true,
		"cycle":     now.Format("2006-01"),
		"items":     out,
		"totals":    totals, // por producto: copilot, actions, git_lfs, …
	})
}
