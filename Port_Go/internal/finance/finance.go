// Package finance handles balance checks, currency conversion and projections.
package finance

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"translate_llm/internal/config"
)

// CheckOpenRouterBalance fetches the OpenRouter balance and an optional
// exchange rate relative to USD.
func CheckOpenRouterBalance(apiKey, targetCurrency string, out io.Writer) (*decimal.Decimal, *decimal.Decimal) {
	if apiKey == "" {
		return nil, nil
	}
	fmt.Fprintln(out, "\nFetching OpenRouter balance...")

	headers := map[string]string{
		"Authorization": "Bearer " + apiKey,
		"HTTP-Referer":  "https://pepin208.dedyn.io",
		"X-Title":       "LLMT",
	}

	var remaining *decimal.Decimal
	if data, err := httpGetJSON("https://openrouter.ai/api/v1/credits", headers, 10*time.Second); err == nil {
		inner, _ := data["data"].(map[string]any)
		total := decimal.NewFromFloat(asFloat(inner["total_credits"]))
		usage := decimal.NewFromFloat(asFloat(inner["total_usage"]))
		r := total.Sub(usage)
		remaining = &r
	} else {
		fmt.Fprintf(out, "Warning: Failed to fetch OpenRouter balance: %v\n", err)
	}

	targetCurrency = strings.ToUpper(strings.TrimSpace(targetCurrency))
	var exchangeRate *decimal.Decimal

	if targetCurrency != "" && targetCurrency != "USD" {
		fmt.Fprintf(out, "Fetching %s exchange rate...\n", targetCurrency)
		if data, err := httpGetJSON("https://open.er-api.com/v6/latest/USD", nil, 10*time.Second); err == nil {
			rates, _ := data["rates"].(map[string]any)
			if v, ok := rates[targetCurrency]; ok {
				r := decimal.NewFromFloat(asFloat(v))
				exchangeRate = &r
			} else {
				fmt.Fprintf(out, "Warning: Currency '%s' not found.\n", targetCurrency)
			}
		} else {
			fmt.Fprintf(out, "Warning: Failed to fetch exchange rate: %v\n", err)
		}
	}

	if remaining != nil {
		if exchangeRate != nil {
			local := remaining.Mul(*exchangeRate)
			fmt.Fprintf(out, "\nInitial OpenRouter Balance: $%s USD / $%s %s\n",
				remaining.StringFixed(6), local.StringFixed(6), targetCurrency)
		} else {
			fmt.Fprintf(out, "\nInitial OpenRouter Balance: $%s USD\n", remaining.StringFixed(6))
		}
	} else if exchangeRate != nil {
		fmt.Fprintf(out, "\nExchange Rate (USD -> %s): %s\n", targetCurrency, exchangeRate.String())
	}
	return remaining, exchangeRate
}

// PrintBatchSummary prints the end-of-batch report and returns the updated balance.
func PrintBatchSummary(
	filesProcessed, statP, statJ, statL, pTokens, cTokens int,
	cost float64,
	initialUSD, exchangeRate *decimal.Decimal,
	targetCurrency string,
	session *config.TranslationSession,
	out io.Writer,
) *decimal.Decimal {
	fmt.Fprintf(out, "\n%s\nBatch Processing Complete\n%s\n", strings.Repeat("=", 50), strings.Repeat("=", 50))
	fmt.Fprintf(out, "Files Processed: %d\n", filesProcessed)
	fmt.Fprintf(out, "Batch Parser stats: Primary JSON [%d] batches | JSON fallback [%d] batches | Legacy fallback [%d] batches\n",
		statP, statJ, statL)
	if session != nil && session.TotalCachedTokens > 0 {
		fmt.Fprintf(out, "⚡ Prompt Caching Stats: %d tokens read from cache\n", session.TotalCachedTokens)
	}
	fmt.Fprintln(out, "══════════════════════════════════════════════════")

	costDec := decimal.NewFromFloat(cost)
	if exchangeRate != nil {
		local := costDec.Mul(*exchangeRate)
		fmt.Fprintf(out, "BATCH COMPLETE | Files: %d | Prompt: %d | Completion: %d | Total cost: $%s USD / $%s %s\n",
			filesProcessed, pTokens, cTokens, costDec.StringFixed(6), local.StringFixed(4), targetCurrency)
	} else {
		fmt.Fprintf(out, "BATCH COMPLETE | Files: %d | Prompt: %d | Completion: %d | Total cost: $%s USD\n",
			filesProcessed, pTokens, cTokens, costDec.StringFixed(6))
	}

	var updated *decimal.Decimal = initialUSD
	if initialUSD != nil {
		u := initialUSD.Sub(costDec)
		updated = &u
		if exchangeRate != nil {
			local := u.Mul(*exchangeRate)
			fmt.Fprintf(out, "Remaining balance: $%s USD / $%s %s\n", u.StringFixed(6), local.StringFixed(2), targetCurrency)
		} else {
			fmt.Fprintf(out, "Remaining balance: $%s USD\n", u.StringFixed(6))
		}
	}
	fmt.Fprintln(out, "══════════════════════════════════════════════════")
	return updated
}

// DisplayFinancialProjection prints the ideal/worst cost estimate for a file.
func DisplayFinancialProjection(filename string, cIdeal, cWorst float64, exchangeRate *decimal.Decimal, targetCurrency string, out io.Writer) {
	fmt.Fprintf(out, "\n--- Financial Projection [%s] ---\n", filename)
	ideal := decimal.NewFromFloat(cIdeal)
	worst := decimal.NewFromFloat(cWorst)
	if exchangeRate != nil {
		idealLocal := ideal.Mul(*exchangeRate)
		worstLocal := worst.Mul(*exchangeRate)
		fmt.Fprintf(out, "Ideal Case:  $%s USD / $%s %s\n", ideal.StringFixed(6), idealLocal.StringFixed(4), targetCurrency)
		fmt.Fprintf(out, "Worst Case:  $%s USD / $%s %s\n", worst.StringFixed(6), worstLocal.StringFixed(4), targetCurrency)
	} else {
		fmt.Fprintf(out, "Ideal Case:  $%s USD\n", ideal.StringFixed(6))
		fmt.Fprintf(out, "Worst Case:  $%s USD\n", worst.StringFixed(6))
	}
	fmt.Fprintln(out, "---------------------------------------")
}

// DisplayGrandTotalProjection prints the aggregate estimate.
func DisplayGrandTotalProjection(totalIdeal, totalWorst float64, exchangeRate *decimal.Decimal, targetCurrency string, out io.Writer) {
	fmt.Fprintf(out, "\n%s\nBatch Grand Total Projection\n%s\n", strings.Repeat("=", 50), strings.Repeat("=", 50))
	ideal := decimal.NewFromFloat(totalIdeal)
	worst := decimal.NewFromFloat(totalWorst)
	if exchangeRate != nil {
		fmt.Fprintf(out, "Total Ideal Case:  $%s USD / $%s %s\n", ideal.StringFixed(6), ideal.Mul(*exchangeRate).StringFixed(4), targetCurrency)
		fmt.Fprintf(out, "Total Worst Case:  $%s USD / $%s %s\n", worst.StringFixed(6), worst.Mul(*exchangeRate).StringFixed(4), targetCurrency)
	} else {
		fmt.Fprintf(out, "Total Ideal Case:  $%s USD\n", ideal.StringFixed(6))
		fmt.Fprintf(out, "Total Worst Case:  $%s USD\n", worst.StringFixed(6))
	}
	fmt.Fprintln(out, strings.Repeat("=", 50))
}

// --- helpers ---------------------------------------------------------------

func httpGetJSON(url string, headers map[string]string, timeout time.Duration) (map[string]any, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	out := map[string]any{}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}

func asFloat(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case string:
		f, _ := decimal.NewFromString(n)
		return f.InexactFloat64()
	case json.Number:
		f, _ := n.Float64()
		return f
	}
	return 0
}
