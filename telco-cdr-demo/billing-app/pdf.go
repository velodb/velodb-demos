package main

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/go-pdf/fpdf"
)

func num(m map[string]any, k string) float64 {
	switch v := m[k].(type) {
	case float64:
		return v
	case int64:
		return float64(v)
	case string:
		var f float64
		fmt.Sscan(v, &f)
		return f
	}
	return 0
}

func str(m map[string]any, k string) string {
	if v, ok := m[k]; ok && v != nil {
		return fmt.Sprint(v)
	}
	return ""
}

func money(f float64) string {
	neg := f < 0
	if neg {
		f = -f
	}
	s := fmt.Sprintf("%.2f", f)
	intPart, frac := s[:len(s)-3], s[len(s)-3:]
	var b strings.Builder
	for i, c := range intPart {
		if i > 0 && (len(intPart)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	if neg {
		return "-$" + b.String() + frac
	}
	return "$" + b.String() + frac
}

func grouped(f float64) string {
	return strings.TrimPrefix(strings.TrimSuffix(money(f), ".00"), "$")
}

func fmtMSISDN(s string) string {
	if len(s) == 10 && strings.HasPrefix(s, "65") {
		return "+65 " + s[2:6] + " " + s[6:]
	}
	return s
}

func renderBillPDF(w io.Writer, month string, d *BillDetail) error {
	b := d.Bill
	period, _ := time.Parse("2006-01", month)
	periodEnd := period.AddDate(0, 1, -1)
	postpaid := str(b, "subscriber_type") == "POSTPAID"
	title := "Monthly Bill"
	if !postpaid {
		title = "Usage Statement"
	}

	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.SetMargins(15, 15, 15)
	pdf.SetAutoPageBreak(true, 18)
	pdf.AliasNbPages("")
	tr := pdf.UnicodeTranslatorFromDescriptor("")
	pdf.SetFooterFunc(func() {
		pdf.SetY(-12)
		pdf.SetFont("Helvetica", "", 7)
		pdf.SetTextColor(130, 130, 140)
		pdf.CellFormat(0, 5, tr(fmt.Sprintf("VeloTel Mobile (demo operator)  |  rated in Apache Doris / VeloDB, database %s, run %s  |  page %d/{nb}",
			d.Database, str(b, "bill_run_id"), pdf.PageNo())), "", 0, "C", false, 0, "")
	})
	pdf.AddPage()

	// header band
	pdf.SetFillColor(17, 24, 39)
	pdf.Rect(0, 0, 210, 38, "F")
	pdf.SetFillColor(34, 211, 238)
	pdf.Rect(0, 38, 210, 1.2, "F")
	pdf.SetXY(15, 11)
	pdf.SetTextColor(255, 255, 255)
	pdf.SetFont("Helvetica", "B", 20)
	pdf.CellFormat(100, 9, "VeloTel Mobile", "", 1, "L", false, 0, "")
	pdf.SetX(15)
	pdf.SetFont("Helvetica", "", 9)
	pdf.SetTextColor(165, 180, 200)
	pdf.CellFormat(100, 6, tr("Voice | Data | Roaming"), "", 0, "L", false, 0, "")
	pdf.SetXY(110, 11)
	pdf.SetFont("Helvetica", "B", 15)
	pdf.SetTextColor(255, 255, 255)
	pdf.CellFormat(85, 9, title, "", 1, "R", false, 0, "")
	pdf.SetX(110)
	pdf.SetFont("Helvetica", "", 9)
	pdf.SetTextColor(165, 180, 200)
	pdf.CellFormat(85, 6, "Bill no. "+str(b, "bill_no"), "", 1, "R", false, 0, "")

	// account + amount due
	pdf.SetXY(15, 47)
	pdf.SetTextColor(20, 20, 30)
	kv := func(k, v string) {
		pdf.SetX(15)
		pdf.SetFont("Helvetica", "", 9)
		pdf.SetTextColor(110, 110, 125)
		pdf.CellFormat(32, 6, k, "", 0, "L", false, 0, "")
		pdf.SetFont("Helvetica", "B", 9)
		pdf.SetTextColor(20, 20, 30)
		pdf.CellFormat(70, 6, tr(v), "", 1, "L", false, 0, "")
	}
	kv("Mobile number", fmtMSISDN(str(b, "msisdn")))
	kv("Rate plan", str(b, "rate_plan")+"  ("+strings.ToLower(str(b, "subscriber_type"))+")")
	kv("Billing period", period.Format("2 Jan 2006")+" - "+periodEnd.Format("2 Jan 2006"))
	kv("Issued", time.Now().Format("2 Jan 2006"))
	if postpaid {
		kv("Payment due", periodEnd.AddDate(0, 0, 21).Format("2 Jan 2006"))
	} else {
		kv("Settlement", "deducted from prepaid balance")
	}

	pdf.SetFillColor(240, 249, 255)
	pdf.SetDrawColor(186, 230, 253)
	pdf.RoundedRect(120, 46, 75, 34, 3, "1234", "FD")
	pdf.SetXY(120, 51)
	pdf.SetFont("Helvetica", "", 9)
	pdf.SetTextColor(3, 105, 161)
	label := "Total amount due"
	if !postpaid {
		label = "Total charged"
	}
	pdf.CellFormat(75, 6, label, "", 1, "C", false, 0, "")
	pdf.SetX(120)
	pdf.SetFont("Helvetica", "B", 24)
	pdf.SetTextColor(12, 74, 110)
	pdf.CellFormat(75, 13, money(num(b, "total")), "", 1, "C", false, 0, "")
	pdf.SetX(120)
	pdf.SetFont("Helvetica", "", 8)
	pdf.SetTextColor(70, 110, 140)
	pdf.CellFormat(75, 5, fmt.Sprintf("incl. GST 9%% %s", money(num(b, "tax"))), "", 1, "C", false, 0, "")

	section := func(y float64, name string) {
		if y > 0 {
			pdf.SetY(y)
		}
		pdf.Ln(4)
		pdf.SetX(15)
		pdf.SetFont("Helvetica", "B", 11)
		pdf.SetTextColor(17, 24, 39)
		pdf.CellFormat(180, 7, tr(name), "", 1, "L", false, 0, "")
		pdf.SetDrawColor(34, 211, 238)
		pdf.SetLineWidth(0.5)
		pdf.Line(15, pdf.GetY(), 195, pdf.GetY())
		pdf.SetLineWidth(0.2)
		pdf.Ln(1.5)
	}

	// charges
	section(84, "Summary of charges")
	widths := []float64{70, 30, 28, 26, 26}
	header := func(cols ...string) {
		pdf.SetFont("Helvetica", "B", 8)
		pdf.SetTextColor(100, 100, 115)
		pdf.SetFillColor(245, 246, 250)
		for i, c := range cols {
			align := "R"
			if i == 0 {
				align = "L"
			}
			pdf.CellFormat(widths[i], 6, c, "", 0, align, true, 0, "")
		}
		pdf.Ln(-1)
	}
	row := func(cols ...string) {
		pdf.SetFont("Helvetica", "", 9)
		pdf.SetTextColor(30, 30, 40)
		for i, c := range cols {
			align := "R"
			if i == 0 {
				align = "L"
			}
			if i == len(cols)-1 {
				pdf.SetFont("Helvetica", "B", 9)
			}
			pdf.CellFormat(widths[i], 6.2, tr(c), "B", 0, align, false, 0, "")
		}
		pdf.Ln(-1)
	}
	pdf.SetDrawColor(232, 234, 240)
	header("Item", "Used", "Included", "Billable", "Amount")
	row("Monthly plan fee - "+str(b, "rate_plan"), "", "", "", money(num(b, "monthly_fee")))
	row("Domestic voice (min)", grouped(num(b, "dom_voice_min")), grouped(num(b, "incl_voice_min")), grouped(num(b, "voice_overage_min")), money(num(b, "voice_overage_charge")))
	row("SMS", grouped(num(b, "sms_sent")), grouped(num(b, "incl_sms")), grouped(num(b, "sms_overage")), money(num(b, "sms_charge")))
	row("Mobile data (MB, after zero-rating)", grouped(num(b, "chargeable_mb")), grouped(num(b, "incl_data_mb")), grouped(num(b, "data_overage_mb")), money(num(b, "data_overage_charge")))
	row(fmt.Sprintf("International calls (min @ %s)", money(num(d.Plan, "intl_per_min"))), grouped(num(b, "intl_min")), "", grouped(num(b, "intl_min")), money(num(b, "intl_charge")))
	row(fmt.Sprintf("Premium numbers (min @ %s)", money(num(d.Plan, "premium_per_min"))), grouped(num(b, "premium_min")), "", grouped(num(b, "premium_min")), money(num(b, "premium_charge")))
	row(fmt.Sprintf("Roaming voice (min @ %s)", money(num(d.Plan, "roaming_voice_per_min"))), grouped(num(b, "roam_voice_min")), "", grouped(num(b, "roam_voice_min")), money(num(b, "roam_voice_charge")))
	row("Roaming SMS", grouped(num(b, "roam_sms")), "", grouped(num(b, "roam_sms")), money(num(b, "roam_sms_charge")))
	roamRate := num(d.Plan, "roaming_data_per_mb")
	row(fmt.Sprintf("Roaming data (MB @ %s/MB)", money(roamRate)), grouped(num(b, "roam_data_mb")), "", grouped(num(b, "roam_data_mb")), money(num(b, "roam_data_charge")))
	if uncapped := num(b, "roam_data_mb") * roamRate; d.Plan != nil && uncapped > num(b, "roam_data_charge")+0.005 {
		// the cap changes the amount: show the arithmetic so the MB figure is not read as dollars
		pdf.SetX(15)
		pdf.SetFont("Helvetica", "I", 8)
		pdf.SetTextColor(3, 105, 161)
		pdf.CellFormat(180, 5.5, tr(fmt.Sprintf("    %s MB x %s/MB = %s, capped at your plan's roaming data limit of %s (you save %s)",
			grouped(num(b, "roam_data_mb")), money(roamRate), money(uncapped), money(num(d.Plan, "roaming_data_cap")),
			money(uncapped-num(b, "roam_data_charge")))), "B", 1, "L", false, 0, "")
	}
	totalLine := func(k string, v float64, bold bool) {
		pdf.SetX(15 + widths[0] + widths[1])
		style := ""
		if bold {
			style = "B"
		}
		pdf.SetFont("Helvetica", style, 9)
		pdf.SetTextColor(30, 30, 40)
		pdf.CellFormat(widths[2]+widths[3], 6.2, k, "", 0, "R", false, 0, "")
		pdf.CellFormat(widths[4], 6.2, money(v), "", 1, "R", false, 0, "")
	}
	pdf.Ln(1)
	totalLine("Subtotal", num(b, "subtotal"), false)
	totalLine("GST 9%", num(b, "tax"), false)
	totalLine("Total", num(b, "total"), true)

	// data usage by category
	section(0, "Data usage by application")
	pdf.SetFont("Helvetica", "", 8)
	zeroRated := fmt.Sprintf("%s MB of %s MB was zero-rated on your plan", grouped(num(b, "zero_rated_mb")), grouped(num(b, "data_total_mb")))
	pdf.SetTextColor(110, 110, 125)
	pdf.CellFormat(180, 5, zeroRated, "", 1, "L", false, 0, "")
	maxMB := 1.0
	for _, a := range d.Apps {
		maxMB = max(maxMB, num(a, "mb"))
	}
	for i, a := range d.Apps {
		if i >= 10 {
			break
		}
		y := pdf.GetY() + 0.8
		pdf.SetX(15)
		pdf.SetFont("Helvetica", "", 8)
		pdf.SetTextColor(40, 40, 50)
		pdf.CellFormat(38, 5, tr(str(a, "app_category")), "", 0, "L", false, 0, "")
		barW := 110 * num(a, "mb") / maxMB
		pdf.SetFillColor(34, 211, 238)
		pdf.Rect(53, y+0.6, max(barW, 0.4), 3.4, "F")
		pdf.SetX(165)
		pdf.CellFormat(30, 5, grouped(num(a, "mb"))+" MB", "", 1, "R", false, 0, "")
	}

	// itemised
	section(0, "Itemised international, premium and roaming usage")
	if len(d.Items) == 0 {
		pdf.SetFont("Helvetica", "I", 9)
		pdf.SetTextColor(110, 110, 125)
		pdf.CellFormat(180, 7, "No itemised usage this period.", "", 1, "L", false, 0, "")
	} else {
		widths = []float64{36, 36, 34, 26, 24, 24}
		header("Date / time", "Number", "Type", "Network", "Duration", "Amount")
		for _, it := range d.Items {
			kind := str(it, "call_type")
			if kind != "SMS" {
				kind = strings.Title(strings.ToLower(str(it, "other_party_type"))) + " " + strings.ToLower(str(it, "direction"))
			}
			network := "home"
			if str(it, "roaming") == "1" {
				network = "roam " + str(it, "visited_plmn")
			}
			dur := "1 SMS"
			if str(it, "call_type") != "SMS" {
				s := int(num(it, "duration_sec"))
				dur = fmt.Sprintf("%d:%02d", s/60, s%60)
			}
			ts := str(it, "start_time")
			if len(ts) > 16 {
				ts = ts[:16]
			}
			row(ts, str(it, "other_party_number"), kind, network, dur, money(num(it, "charge")))
		}
		if len(d.Items) == 300 {
			pdf.SetFont("Helvetica", "I", 8)
			pdf.CellFormat(180, 6, "(first 300 records shown)", "", 1, "L", false, 0, "")
		}
	}
	return pdf.Output(w)
}
