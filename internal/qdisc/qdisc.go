package qdisc

import (
	"fmt"

	"github.com/vishvananda/netlink"
)

type LinkInfo struct {
	Name          string
	RootQdiscType string
}

// Manager abstracts netlink so tests can inject a fake.
type Manager interface {
	ListLinks() ([]LinkInfo, error)
	ReplaceRootWithPfifoFast(dev string) error
}

// ManagerFactory builds a Manager for one netns visit, after the thread has entered it.
type ManagerFactory func() Manager

type Result struct {
	Replaced     int
	WouldReplace int
	Skipped      int
}

// Apply replaces an fq root qdisc with pfifo_fast on every Kata tap in the
// current netns, or only counts them when dryRun is set.
func Apply(m Manager, dryRun bool) (Result, error) {
	var res Result
	links, err := m.ListLinks()
	if err != nil {
		return res, fmt.Errorf("list links: %w", err)
	}
	for _, link := range links {
		if !IsKataTapDevice(link.Name) {
			continue
		}
		if link.RootQdiscType != "fq" {
			res.Skipped++
			continue
		}
		if dryRun {
			res.WouldReplace++
			continue
		}
		if err := m.ReplaceRootWithPfifoFast(link.Name); err != nil {
			return res, fmt.Errorf("replace qdisc on %s: %w", link.Name, err)
		}
		res.Replaced++
	}
	return res, nil
}

// netlinkManager acts on the calling thread's netns.
type netlinkManager struct{}

func NewManager() Manager { return netlinkManager{} }

func (netlinkManager) ListLinks() ([]LinkInfo, error) {
	links, err := netlink.LinkList()
	if err != nil {
		return nil, err
	}
	out := make([]LinkInfo, 0, len(links))
	for _, l := range links {
		root := ""
		qdiscs, qerr := netlink.QdiscList(l)
		if qerr == nil {
			for _, q := range qdiscs {
				if q.Attrs().Parent == netlink.HANDLE_ROOT {
					root = q.Type()
					break
				}
			}
		}
		out = append(out, LinkInfo{Name: l.Attrs().Name, RootQdiscType: root})
	}
	return out, nil
}

func (netlinkManager) ReplaceRootWithPfifoFast(dev string) error {
	link, err := netlink.LinkByName(dev)
	if err != nil {
		return fmt.Errorf("link by name %s: %w", dev, err)
	}
	q := &netlink.GenericQdisc{
		QdiscAttrs: netlink.QdiscAttrs{
			LinkIndex: link.Attrs().Index,
			Parent:    netlink.HANDLE_ROOT,
		},
		QdiscType: "pfifo_fast",
	}
	return netlink.QdiscReplace(q)
}
