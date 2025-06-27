package config

const DefaultLatencyPolicyFunc = `
	function sortUpstreams(upstreamData: UpstreamData[]): string[] {
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
	
		return upstreamData.map((data, index) => {
			const score = (scoreFunc(1 - normalizedLatencies[index]) * 1.5) + scoreFunc(1 - normalizedTotalRequests[index])
			return {
				"id": data.id,
				"score": score
			}
		}).sort((a, b) => b.score - a.score).map(data => data.id)
	}
`
