package config

const defaultLatencyPolicyFunc = `
	function sortUpstreams(upstreamData: UpstreamData[]): SortResponse {
		const normalizeValuesFunc = (values: number[]): number[] => {
			if (values.length === 0) {
				return []
			}
			const max = Math.max(...values)
	
			return values.map((x: number) => max > 0 ? x / max : 0)
		}
		const scoreFunc = (value: number): number => {
			return Math.pow(value, 2)
		}
	
		const normalizedLatencies = normalizeValuesFunc(upstreamData.map((data) => data.metrics.latencyP90))
		const normalizedTotalRequests = normalizeValuesFunc(upstreamData.map((data) => data.metrics.totalRequests))

		const scores = upstreamData.map((data, index) => {
			const score = (scoreFunc(1 - normalizedLatencies[index]) * 1.5) + scoreFunc(1 - normalizedTotalRequests[index])
			return {
				"id": data.id,
				"score": score
			}
		})
		
		return {
        	sortedUpstreams: scores.sort((a, b) => b.score - a.score).map(data => data.id),
        	scores: scores
    	}
	}
`

const defaultLatencyErrorRatePolicyFunc = `
	function sortUpstreams(upstreamData: UpstreamData[]): SortResponse {
		const normalizeValuesFunc = (values: number[]): number[] => {
			if (values.length === 0) {
				return []
			}
			const max = Math.max(...values)
	
			return values.map((x: number) => max > 0 ? x / max : 0)
		}
		const scoreFunc = (value: number): number => {
			return Math.pow(value, 2)
		}
	
		const normalizedLatencies = normalizeValuesFunc(upstreamData.map((data) => data.metrics.latencyP90))
		const normalizedTotalRequests = normalizeValuesFunc(upstreamData.map((data) => data.metrics.totalRequests))
		const normalizedErrorRates = normalizeValuesFunc(upstreamData.map((data) => data.metrics.errorRate))

		const scores = upstreamData.map((data, index) => {
			const score = (scoreFunc(1 - normalizedLatencies[index]) * 2) + scoreFunc(1 - normalizedTotalRequests[index]) + (scoreFunc(1 - normalizedErrorRates[index]) * 1.5)
			return {
				"id": data.id,
				"score": score
			}
		})
		
		return {
        	sortedUpstreams: scores.sort((a, b) => b.score - a.score).map(data => data.id),
        	scores: scores
    	}
	}
`

const (
	DefaultLatencyPolicyFuncName          = "defaultLatencyPolicyFunc"
	DefaultLatencyErrorRatePolicyFuncName = "defaultLatencyErrorRatePolicyFunc"
)

var defaultRatingFunctions = map[string]string{
	DefaultLatencyPolicyFuncName:          defaultLatencyPolicyFunc,
	DefaultLatencyErrorRatePolicyFuncName: defaultLatencyErrorRatePolicyFunc,
}
