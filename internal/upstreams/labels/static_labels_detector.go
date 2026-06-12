package labels

type StaticLabelsDetector struct {
	labels map[string]string
}

func NewStaticLabelsDetector(labels map[string]string) *StaticLabelsDetector {
	copyLabels := make(map[string]string, len(labels))
	for k, v := range labels {
		copyLabels[k] = v
	}
	return &StaticLabelsDetector{labels: copyLabels}
}

func (s *StaticLabelsDetector) DetectLabels() map[string]string {
	copyLabels := make(map[string]string, len(s.labels))
	for k, v := range s.labels {
		copyLabels[k] = v
	}
	return copyLabels
}

var _ LabelsDetector = (*StaticLabelsDetector)(nil)
