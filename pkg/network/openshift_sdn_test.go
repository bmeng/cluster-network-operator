package network

import (
	"testing"

	yaml "github.com/ghodss/yaml"

	netv1 "github.com/openshift/cluster-network-operator/pkg/apis/networkoperator/v1"
	"github.com/openshift/cluster-network-operator/pkg/apply"

	uns "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	. "github.com/onsi/gomega"
)

var OpenShiftSDNConfig = netv1.NetworkConfig{
	Spec: netv1.NetworkConfigSpec{
		ServiceNetwork: "172.30.0.0/16",
		ClusterNetworks: []netv1.ClusterNetwork{
			{
				CIDR:             "10.128.0.0/15",
				HostSubnetLength: 9,
			},
			{
				CIDR:             "10.0.0.0/14",
				HostSubnetLength: 8,
			},
		},
		DefaultNetwork: netv1.DefaultNetworkDefinition{
			Type: netv1.NetworkTypeOpenShiftSDN,
			OpenShiftSDNConfig: &netv1.OpenShiftSDNConfig{
				Mode: netv1.SDNModeNetworkPolicy,
			},
		},
	},
}

var manifestDir = "../../bindata"

// TestRenderOpenShiftSDN has some simple rendering tests
func TestRenderOpenShiftSDN(t *testing.T) {
	g := NewGomegaWithT(t)

	crd := OpenShiftSDNConfig.DeepCopy()
	config := &crd.Spec
	sdnConfig := config.DefaultNetwork.OpenShiftSDNConfig

	errs := validateOpenShiftSDN(config)
	g.Expect(errs).To(HaveLen(0))
	FillDefaults(config, nil)

	// Make sure the OVS daemonset isn't created
	truth := true
	sdnConfig.UseExternalOpenvswitch = &truth
	objs, err := renderOpenShiftSDN(config, manifestDir)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(objs).NotTo(ContainElement(HaveKubernetesID("DaemonSet", "openshift-sdn", "ovs")))

	// enable openvswitch
	sdnConfig.UseExternalOpenvswitch = nil
	objs, err = renderOpenShiftSDN(config, manifestDir)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(objs).To(ContainElement(HaveKubernetesID("DaemonSet", "openshift-sdn", "ovs")))

	// It's important that the namespace is first
	g.Expect(objs[0]).To(HaveKubernetesID("Namespace", "", "openshift-sdn"))
	g.Expect(objs).To(ContainElement(HaveKubernetesID("ClusterRole", "", "openshift-sdn")))
	g.Expect(objs).To(ContainElement(HaveKubernetesID("ClusterRole", "", "openshift-sdn-controller")))
	g.Expect(objs).To(ContainElement(HaveKubernetesID("ServiceAccount", "openshift-sdn", "sdn")))
	g.Expect(objs).To(ContainElement(HaveKubernetesID("ServiceAccount", "openshift-sdn", "sdn-controller")))
	g.Expect(objs).To(ContainElement(HaveKubernetesID("ClusterRoleBinding", "", "openshift-sdn")))
	g.Expect(objs).To(ContainElement(HaveKubernetesID("DaemonSet", "openshift-sdn", "sdn")))
	g.Expect(objs).To(ContainElement(HaveKubernetesID("DaemonSet", "openshift-sdn", "sdn-controller")))

	// make sure all deployments are in the master
	for _, obj := range objs {
		if obj.GetKind() != "Deployment" {
			continue
		}

		sel, found, err := uns.NestedStringMap(obj.Object, "spec", "template", "spec", "nodeSelector")
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(found).To(BeTrue())

		_, ok := sel["node-role.kubernetes.io/master"]
		g.Expect(ok).To(BeTrue())
	}

	// Make sure every obj is reasonable:
	// - it is supported
	// - it reconciles to itself (steady state)
	for _, obj := range objs {
		g.Expect(apply.IsObjectSupported(obj)).NotTo(HaveOccurred())
		cur := obj.DeepCopy()
		upd := obj.DeepCopy()

		err = apply.MergeObjectForUpdate(cur, upd)
		g.Expect(err).NotTo(HaveOccurred())

		tweakMetaForCompare(cur)
		g.Expect(cur).To(Equal(upd))
	}
}

func TestFillOpenShiftSDNDefaults(t *testing.T) {
	g := NewGomegaWithT(t)

	crd := OpenShiftSDNConfig.DeepCopy()
	conf := &crd.Spec
	conf.DefaultNetwork.OpenShiftSDNConfig = nil

	// vars
	f := false
	p := uint32(4789)
	m := uint32(8950)

	expected := netv1.NetworkConfigSpec{
		ServiceNetwork: "172.30.0.0/16",
		ClusterNetworks: []netv1.ClusterNetwork{
			{
				CIDR:             "10.128.0.0/15",
				HostSubnetLength: 9,
			},
			{
				CIDR:             "10.0.0.0/14",
				HostSubnetLength: 8,
			},
		},
		DefaultNetwork: netv1.DefaultNetworkDefinition{
			Type: netv1.NetworkTypeOpenShiftSDN,
			OpenShiftSDNConfig: &netv1.OpenShiftSDNConfig{
				Mode:      netv1.SDNModeNetworkPolicy,
				VXLANPort: &p,
				MTU:       &m,
			},
		},
		DeployKubeProxy: &f,
		KubeProxyConfig: &netv1.ProxyConfig{
			BindAddress: "0.0.0.0",
			ProxyArguments: map[string][]string{"metrics-bind-address": []string{"0.0.0.0:9101"}},
		},
	}

	fillOpenShiftSDNDefaults(conf, nil, 9000)

	g.Expect(conf).To(Equal(&expected))

}

func TestValidateOpenShiftSDN(t *testing.T) {
	g := NewGomegaWithT(t)

	crd := OpenShiftSDNConfig.DeepCopy()
	config := &crd.Spec
	sdnConfig := config.DefaultNetwork.OpenShiftSDNConfig

	err := validateOpenShiftSDN(config)
	g.Expect(err).To(BeEmpty())
	FillDefaults(config, nil)

	errExpect := func(substr string) {
		t.Helper()
		g.Expect(validateOpenShiftSDN(config)).To(
			ContainElement(MatchError(
				ContainSubstring(substr))))
	}

	// set mtu to insanity
	mtu := uint32(70000)
	sdnConfig.MTU = &mtu
	errExpect("invalid MTU 70000")

	sdnConfig.Mode = "broken"
	errExpect("invalid openshift-sdn mode \"broken\"")

	port := uint32(66666)
	sdnConfig.VXLANPort = &port
	errExpect("invalid VXLANPort 66666")

	config.ClusterNetworks = nil
	errExpect("ClusterNetworks cannot be empty")
}

func TestProxyArgs(t *testing.T) {
	g := NewGomegaWithT(t)

	crd := OpenShiftSDNConfig.DeepCopy()
	config := &crd.Spec
	FillDefaults(config, nil)

	// iter through all objects, finding the sdn config map
	getSdnConfigFile := func(objs []*uns.Unstructured) *uns.Unstructured {
		for _, obj := range objs {
			if obj.GetKind() == "ConfigMap" && obj.GetName() == "sdn-config" {
				val, ok, err := uns.NestedString(obj.Object, "data", "sdn-config.yaml")
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(ok).To(BeTrue())

				out := uns.Unstructured{}
				err = yaml.Unmarshal([]byte(val), &out)
				g.Expect(err).NotTo(HaveOccurred())
				t.Logf("%+v", out)

				return &out
			}
		}
		t.Fatal("failed to find sdn-config")
		return nil //unreachable
	}

	// test default rendering
	objs, err := renderOpenShiftSDN(config, manifestDir)
	g.Expect(err).NotTo(HaveOccurred())
	cfg := getSdnConfigFile(objs)
	val, _, _ := uns.NestedString(cfg.Object, "servingInfo", "bindAddress")
	g.Expect(val).To(Equal("0.0.0.0:10251"))
	val, _, _ = uns.NestedString(cfg.Object, "iptablesSyncPeriod")
	g.Expect(val).To(Equal(""))

	// set sync period
	config.KubeProxyConfig = &netv1.ProxyConfig{
		IptablesSyncPeriod: "10s",
		BindAddress:        "1.2.3.4",
	}
	objs, err = renderOpenShiftSDN(config, manifestDir)
	g.Expect(err).NotTo(HaveOccurred())
	cfg = getSdnConfigFile(objs)
	g.Expect(cfg.Object).To(HaveKeyWithValue("iptablesSyncPeriod", "10s"))
	val, _, _ = uns.NestedString(cfg.Object, "servingInfo", "bindAddress")
	g.Expect(val).To(Equal("1.2.3.4:10251"))

	//set proxy args
	config.KubeProxyConfig.ProxyArguments = map[string][]string{
		"a": {"b"},
		"c": {"d", "e"},
	}
	objs, err = renderOpenShiftSDN(config, manifestDir)
	g.Expect(err).NotTo(HaveOccurred())
	cfg = getSdnConfigFile(objs)

	arg, _, _ := uns.NestedStringSlice(cfg.Object, "proxyArguments", "a")
	g.Expect(arg).To(Equal([]string{"b"}))

	arg, _, _ = uns.NestedStringSlice(cfg.Object, "proxyArguments", "c")
	g.Expect(arg).To(Equal([]string{"d", "e"}))

}

func TestOpenShiftSDNIsSafe(t *testing.T) {
	g := NewGomegaWithT(t)

	prev := OpenShiftSDNConfig.Spec.DeepCopy()
	FillDefaults(prev, nil)
	next := OpenShiftSDNConfig.Spec.DeepCopy()
	FillDefaults(next, nil)

	errs := isOpenShiftSDNChangeSafe(prev, next)
	g.Expect(errs).To(BeEmpty())

	// change the vxlan port
	p := uint32(99)
	next.DefaultNetwork.OpenShiftSDNConfig.VXLANPort = &p

	errs = isOpenShiftSDNChangeSafe(prev, next)
	g.Expect(errs).To(HaveLen(1))
	g.Expect(errs[0]).To(MatchError("cannot change openshift-sdn configuration"))
}
