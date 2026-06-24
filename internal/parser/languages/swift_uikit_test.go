package languages

import (
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

func uikitRole(nodes []*graph.Node, name string) (string, bool) {
	for _, n := range nodes {
		if n.Kind == graph.KindType && n.Name == name && n.Meta != nil {
			if r, ok := n.Meta["uikit_role"].(string); ok {
				return r, true
			}
		}
	}
	return "", false
}

func TestUIKit_SwiftSubclassClassification(t *testing.T) {
	src := []byte(`import UIKit

class HomeVC: UIViewController {
    override func viewDidLoad() {}
}

class UserCell: UITableViewCell {}

class BadgeView: UIView {}

class Plain {}
`)
	res, err := NewSwiftExtractor().Extract("UI/Home.swift", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if r, _ := uikitRole(res.Nodes, "HomeVC"); r != "view_controller" {
		t.Errorf("HomeVC uikit_role = %q (want view_controller)", r)
	}
	if r, _ := uikitRole(res.Nodes, "UserCell"); r != "cell" {
		t.Errorf("UserCell uikit_role = %q (want cell)", r)
	}
	if r, _ := uikitRole(res.Nodes, "BadgeView"); r != "view" {
		t.Errorf("BadgeView uikit_role = %q (want view)", r)
	}
	if r, ok := uikitRole(res.Nodes, "Plain"); ok {
		t.Errorf("plain class should carry no uikit_role, got %q", r)
	}
}

func TestUIKit_ObjCSubclassClassification(t *testing.T) {
	src := []byte(`#import <UIKit/UIKit.h>

@interface HomeViewController : UIViewController
@end

@interface AvatarCell : UICollectionViewCell
@end
`)
	res, err := NewObjCExtractor().Extract("UI/Home.h", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if r, _ := uikitRole(res.Nodes, "HomeViewController"); r != "view_controller" {
		t.Errorf("ObjC HomeViewController uikit_role = %q (want view_controller)", r)
	}
	if r, _ := uikitRole(res.Nodes, "AvatarCell"); r != "cell" {
		t.Errorf("ObjC AvatarCell uikit_role = %q (want cell)", r)
	}
}
