package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

type TargetRef struct {
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
	// +kubebuilder:validation:MinLength=1
	NodeSet string `json:"nodeSet"`
}
type Schedule struct {
	Name     string `json:"name"`
	TimeZone string `json:"timeZone"`
	// Days use Sunday=0 through Saturday=6; day refers to the window start.
	// +kubebuilder:validation:MinItems=1
	Days []int `json:"days"`
	// +kubebuilder:validation:Pattern=`^([01][0-9]|2[0-3]):[0-5][0-9]$`
	Start string `json:"start"`
	// +kubebuilder:validation:Pattern=`^([01][0-9]|2[0-3]):[0-5][0-9]$`
	End string `json:"end"`
	// +kubebuilder:validation:Minimum=1
	Replicas int32 `json:"replicas"`
}
type Sources struct {
	// +kubebuilder:validation:MaxItems=64
	Schedules []Schedule `json:"schedules,omitempty"`
}

// +kubebuilder:validation:XValidation:rule="self.maxReplicas >= self.minReplicas",message="maxReplicas must be >= minReplicas"
type Scaling struct {
	// +kubebuilder:validation:Minimum=1
	MinReplicas int32 `json:"minReplicas"`
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=10000
	MaxReplicas int32 `json:"maxReplicas"`
	// +kubebuilder:validation:Minimum=1
	MaxStep int32 `json:"maxStep"`
	// +kubebuilder:validation:Minimum=15
	PollSeconds int32 `json:"pollSeconds"`
	// +kubebuilder:validation:Minimum=0
	StabilizationSeconds int32 `json:"stabilizationSeconds"`
	// +kubebuilder:validation:Minimum=60
	CooldownSeconds int32 `json:"cooldownSeconds"`
	// Convergence timeout reports a stall. It NEVER rolls count back.
	// +kubebuilder:default=2700
	// +kubebuilder:validation:Minimum=60
	ConvergenceTimeoutSeconds int32 `json:"convergenceTimeoutSeconds,omitempty"`
}
type Safety struct {
	// Require each selected NodeSet Pod to run on a different Kubernetes node.
	RequireDistinctNodes bool `json:"requireDistinctNodes,omitempty"`
	// +kubebuilder:validation:Enum=green;yellow
	RequiredHealth string `json:"requiredHealth"`
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=100
	MaxDiskUsedPercent int32 `json:"maxDiskUsedPercent"`
	// +kubebuilder:validation:Minimum=1
	MaxShardsPerNode int32 `json:"maxShardsPerNode"`
	// +kubebuilder:validation:Enum=true
	BlockScaleDown bool `json:"blockScaleDown"`
}

// +kubebuilder:validation:XValidation:rule="self.targetRef == oldSelf.targetRef",message="targetRef is immutable; create another autoscaler"
type ElasticsearchQueueAutoscalerSpec struct {
	TargetRef TargetRef `json:"targetRef"`
	Sources   Sources   `json:"sources,omitempty"`
	// KEDA/HPA owns requestedReplicas through /scale. The operator never writes it.
	// +kubebuilder:default=KEDA
	// +kubebuilder:validation:Enum=KEDA
	Provider string `json:"provider,omitempty"`
	// +kubebuilder:default=1
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=10000
	RequestedReplicas int32   `json:"requestedReplicas"`
	Scaling           Scaling `json:"scaling"`
	Safety            Safety  `json:"safety"`
	// +kubebuilder:default=Audit
	// +kubebuilder:validation:Enum=Audit;DryRun;Auto
	Mode string `json:"mode,omitempty"`
}
type ElasticsearchQueueAutoscalerStatus struct {
	ObservedGeneration  int64        `json:"observedGeneration,omitempty"`
	CurrentReplicas     int32        `json:"currentReplicas,omitempty"`
	DesiredReplicas     int32        `json:"desiredReplicas,omitempty"`
	LastDecision        string       `json:"lastDecision,omitempty"`
	LastScaleTime       *metav1.Time `json:"lastScaleTime,omitempty"`
	PendingSince        *metav1.Time `json:"pendingSince,omitempty"`
	CandidateSince      *metav1.Time `json:"candidateSince,omitempty"`
	CandidateReplicas   int32        `json:"candidateReplicas,omitempty"`
	CandidatePolicyHash string       `json:"candidatePolicyHash,omitempty"`
	// Replicas counts non-terminating Pods in the selected NodeSet, not ECK spec.count.
	Replicas      int32  `json:"replicas"`
	ReadyReplicas int32  `json:"readyReplicas"`
	Selector      string `json:"selector"`
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:subresource:scale:specpath=.spec.requestedReplicas,statuspath=.status.replicas,selectorpath=.status.selector
// +kubebuilder:printcolumn:name="Current",type=integer,JSONPath=`.status.currentReplicas`
// +kubebuilder:printcolumn:name="Desired",type=integer,JSONPath=`.status.desiredReplicas`
// +kubebuilder:printcolumn:name="Mode",type=string,JSONPath=`.spec.mode`
type ElasticsearchQueueAutoscaler struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              ElasticsearchQueueAutoscalerSpec   `json:"spec"`
	Status            ElasticsearchQueueAutoscalerStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type ElasticsearchQueueAutoscalerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ElasticsearchQueueAutoscaler `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ElasticsearchQueueAutoscaler{}, &ElasticsearchQueueAutoscalerList{})
}
