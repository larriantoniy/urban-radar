package zakupki

import (
	"fmt"
	"strings"
)

var interestingKeywords = []string{
	"строитель", "реконструк", "капитальн", "дорог", "тротуар", "освещен",
	"благоустрой", "парк", "сквер", "школ", "детск", "сад", "больниц",
	"поликлиник", "медицинск", "оборудован", "спорт", "автобус", "троллейбус",
	"водоснабж", "теплоснабж", "коммунальн", "снос", "демонтаж", "проектирован",
	"техник", "общественн",
}

var noiseKeywords = []string{
	"канцеляр", "бумаг", "перчат", "моющ", "продукт", "питани", "хозяйственн",
	"консультационн", "обучени", "поставка расходн", "уборк",
}

func Evaluate(p Procurement) FilterResult {
	locality := strings.ToLower(strings.Join([]string{p.Object, p.DeliveryPlace, p.Address}, " "))
	customer := strings.ToLower(p.CustomerName)
	result := FilterResult{Relevance: Irrelevant}
	if containsAny(locality, "тольятти", "автозаводский район", "комсомольский район", "центральный район") {
		result.Relevance = Relevant
		result.Reasons = append(result.Reasons, "explicit Togliatti location or institution")
	} else if containsAny(customer, "тольятти") {
		result.Relevance = MaybeRelevant
		result.Reasons = append(result.Reasons, "customer name indicates Togliatti")
	} else if strings.Contains(strings.ToLower(p.CustomerRegion), "самар") {
		result.Reasons = append(result.Reasons, "Samara region only; no concrete Togliatti signal")
	}

	combined := strings.ToLower(strings.Join([]string{p.Object, p.DeliveryPlace, p.Address}, " "))
	for _, keyword := range interestingKeywords {
		if strings.Contains(combined, keyword) {
			result.InterestScore++
			result.CategorySignals = append(result.CategorySignals, keyword)
		}
	}
	for _, keyword := range noiseKeywords {
		if strings.Contains(combined, keyword) {
			result.InterestScore--
			result.Reasons = append(result.Reasons, "routine procurement signal: "+keyword)
		}
	}
	if p.Price >= 10_000_000 {
		result.InterestScore++
		result.Reasons = append(result.Reasons, "large initial price")
	}
	result.Candidate = result.Relevance != Irrelevant && result.InterestScore > 0
	if result.Relevance == Irrelevant {
		result.Candidate = false
	}
	if result.Candidate {
		result.Reasons = append(result.Reasons, fmt.Sprintf("interest score %d", result.InterestScore))
	}
	return result
}

func containsAny(value string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
}
