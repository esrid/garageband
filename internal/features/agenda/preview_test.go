package agenda

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/a-h/templ"
)

// TestWritePreview dumps the agenda screens as static HTML for visual and
// accessibility review. It is a tool, not an assertion: it only runs when
// GARAGEBAND_PREVIEW_DIR points somewhere. The sample day below exists for this
// preview and for the view tests — never for production code.
func TestWritePreview(t *testing.T) {
	dir := os.Getenv("GARAGEBAND_PREVIEW_DIR")
	if dir == "" {
		t.Skip("set GARAGEBAND_PREVIEW_DIR to write preview pages")
	}
	moment := func(hour, minute int) time.Time {
		return time.Date(2026, 3, 12, hour, minute, 0, 0, time.UTC)
	}

	day := Day{
		Organization: "Garage Central", LocationName: "Atelier Gerland",
		Date: moment(0, 0), CanManage: true,
		Appointments: []Appointment{
			{
				ID: "a1", StartsAt: moment(8, 30), EndsAt: moment(10, 0),
				CustomerID: "c1", CustomerName: "Claire Dupont",
				VehicleLabel: "AB-123-CD · Renault Clio", ServiceName: "Révision annuelle",
				ResourceName: "Pont 1", Status: "confirmed", Source: "agent",
				Note: "Signale un bruit au freinage depuis deux semaines.",
			},
			{
				ID: "a2", StartsAt: moment(10, 15), EndsAt: moment(11, 0),
				CustomerID: "c2", CustomerName: "Transports Martin",
				VehicleLabel: "AA-111-AA · Renault Master", ServiceName: "Vidange",
				ResourceName: "Pont 2", Status: "pending", Source: "dashboard",
			},
			{
				ID: "a3", StartsAt: moment(14, 0), EndsAt: moment(16, 30),
				CustomerID: "c3", CustomerName: "Yanis Benali",
				VehicleLabel: "EF-456-GH · Peugeot Partner", ServiceName: "Distribution",
				ResourceName: "Pont 1", Status: "in_progress", Source: "dashboard",
			},
			{
				ID: "a4", StartsAt: moment(17, 0), EndsAt: moment(17, 30),
				CustomerID: "c4", CustomerName: "Sophie Nguyen",
				VehicleLabel: "IJ-789-KL", ServiceName: "Diagnostic",
				ResourceName: "Pont 2", Status: "cancelled", Source: "agent",
			},
		},
	}
	emptyDay := day
	emptyDay.Appointments = nil

	form := FormPage{
		Organization: "Garage Central", LocationName: "Atelier Gerland",
		CanManage: true,
		Customer:  CustomerRef{ID: "c1", Label: "Claire Dupont"},
		Vehicles: []Option{
			{Value: "v1", Label: "AB-123-CD · Renault Clio"},
			{Value: "v2", Label: "EF-456-GH · Peugeot Partner"},
		},
		Services: []Option{
			{Value: "s1", Label: "Révision annuelle · 1 h 30"},
			{Value: "s2", Label: "Vidange · 45 min"},
			{Value: "s3", Label: "Distribution · 2 h 30"},
		},
		Resources: []Option{{Value: "r1", Label: "Pont 1"}, {Value: "r2", Label: "Pont 2"}},
		Values:    FormValues{Date: "2026-03-12", StartTime: "09:00", VehicleID: "v1", ServiceID: "s1"},
	}
	invalid := form
	invalid.Notice = Notice{Kind: NoticeInvalid, Message: "Deux champs demandent une correction."}
	invalid.Values.StartTime = ""
	invalid.FieldErrors = map[string]string{
		FieldStartTime: "Indiquez une heure de début.",
		FieldResource:  "Choisissez la ressource occupée pendant l'intervention.",
	}
	conflict := form
	conflict.Values.ResourceID = "r1"
	conflict.Notice = Notice{
		Kind:    NoticeConflict,
		Message: "Le Pont 1 est déjà occupé de 08:30 à 10:00. Choisissez une autre ressource ou un autre horaire.",
	}
	edit := form
	edit.ID = "a1"
	edit.Cancellable = true
	edit.Values.ResourceID = "r1"
	edit.Values.Note = "Signale un bruit au freinage depuis deux semaines."

	pages := map[string]templ.Component{
		"agenda.html":             Show(day),
		"agenda-empty.html":       Show(emptyDay),
		"agenda-readonly.html":    Show(readOnly(day)),
		"agenda-new.html":         Form(form),
		"agenda-no-customer.html": Form(FormPage{Organization: "Garage Central", LocationName: "Atelier Gerland", CanManage: true}),
		"agenda-invalid.html":     Form(invalid),
		"agenda-conflict.html":    Form(conflict),
		"agenda-edit.html":        Form(edit),
	}
	for name, page := range pages {
		file, err := os.Create(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := page.Render(context.Background(), file); err != nil {
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func readOnly(day Day) Day {
	day.CanManage = false
	return day
}
