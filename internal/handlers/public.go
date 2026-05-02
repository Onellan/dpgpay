package handlers

import (
	"net/http"
	"strings"

	"github.com/gorilla/csrf"
)

type homePageData struct {
	BaseURL   string
	CSRFField any
}

func (a *App) HomePage(w http.ResponseWriter, r *http.Request) {
	a.render(w, "home.html", homePageData{BaseURL: a.requestBaseURL(r), CSRFField: csrf.TemplateField(r)})
}

func (a *App) PortalOpenPayment(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	id := strings.TrimSpace(r.FormValue("payment_id"))
	if id == "" {
		http.Redirect(w, r, "/portal", http.StatusFound)
		return
	}
	http.Redirect(w, r, "/pay/"+id, http.StatusFound)
}
