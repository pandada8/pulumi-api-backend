package main

import (
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

type Item struct {
	pulumi.CustomResourceState
	Value pulumi.StringOutput `pulumi:"value"`
}

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {
		c := config.New(ctx, "")
		var provider pulumi.ProviderResourceState
		err := ctx.RegisterResource("pulumi:providers:backendtest", "test", pulumi.Map{
			"worldURL": pulumi.String(c.Require("worldURL")),
		}, &provider, pulumi.Version("0.0.1"))
		if err != nil {
			return err
		}
		var item Item
		err = ctx.RegisterResource("backendtest:index:Item", "r", pulumi.Map{
			"name":        pulumi.String(ctx.Stack() + "-r"),
			"value":       pulumi.String(c.Require("message")),
			"replaceKey":  pulumi.String(c.Get("replaceKey")),
			"secretValue": c.RequireSecret("password"),
		}, &item, pulumi.Provider(&provider), pulumi.Version("0.0.1"))
		if err != nil {
			return err
		}
		ctx.Export("value", item.Value)
		ctx.Export("password", c.RequireSecret("password"))
		return nil
	})
}
