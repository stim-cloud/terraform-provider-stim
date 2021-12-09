package stim

import (
  "context"
  "fmt"
  "log"
  "time"

  "github.com/google/uuid"

  "github.com/hashicorp/terraform-plugin-sdk/v2/diag"
  "github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
  "github.com/hashicorp/terraform-plugin-sdk/v2/helper/resource"
)

func ResourceCluster() *schema.Resource {
  return &schema.Resource{
    Description: `
    Manages Kubernetes clusters.
    `,
    CreateContext: resourceClusterCreate,
    ReadContext:   resourceClusterRead,
    UpdateContext:   resourceClusterUpdate,
    DeleteContext: resourceClusterDelete,
    Schema: map[string]*schema.Schema{
      "name": {
        Type:        schema.TypeString,
        Required:    true,
        ForceNew:    true,
        Description: "Cluster name. Changing the name forces re-creation.",
      },
      "region": {
        Type:        schema.TypeString,
        Required:    true,
        ForceNew:    true,
        Description: "Cluster region, valid values: [eu-germany-1].",
      },
      "nodes": {
        Type:     schema.TypeList,
        Optional: true,
        Elem: &schema.Resource{
          Schema: map[string]*schema.Schema{
            "id": &schema.Schema{
              Type:     schema.TypeString,
              Computed: true,
            },
            "node_type": &schema.Schema{
              Type:     schema.TypeString,
              ForceNew: true,
              Required: true,
            },
            "quantity": &schema.Schema{
              Type:     schema.TypeInt,
              Required: true,
            },
          },
        },
			},
		},
    Timeouts: &schema.ResourceTimeout{
      Create: schema.DefaultTimeout(10 * time.Minute),
    },
	}
}

type ClusterNodeInput struct {
	NodeType string `json:"nodeTypeName"`
	Quantity int    `json:"quantity"`
}

type PostClusterInput struct {
	Name   string             `json:"name"`
	Region string             `json:"regionName"`
	Nodes  []ClusterNodeInput `json:"nodes"`
}

type PostClusterPayload struct {
	Id   uuid.UUID `json:"id"`
	Name string    `json:"name"`
}

type PutNodePoolByTypeInput struct {
	Quantity int `json:"quantity"`
}

func resourceClusterCreate(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
  log.Printf("[DEBUG] Creating cluster: %s", d.Get("name").(string))
	var diags diag.Diagnostics
	client := meta.(*StimClient)
	api := client.stimApi
  nodes := d.Get("nodes").([]interface{})
  nodeInput := []ClusterNodeInput{}

  for _, node := range nodes {
    i := node.(map[string]interface{})
    no := ClusterNodeInput{
      NodeType: i["node_type"].(string),
      Quantity: i["quantity"].(int),
    }
    nodes = append(nodes, no)
  }

	input := &PostClusterInput{
		Name:   d.Get("name").(string),
		Region: d.Get("region").(string),
		Nodes:  nodeInput,
	}

	resp, err := api.R().SetBody(input).SetResult(PostClusterPayload{}).SetError(StimApiError{}).ForceContentType("application/json").Post("rest/v1/cluster")
	if err != nil {
		return diag.FromErr(err)
	}
	if resp.StatusCode() != 200 {
		stimErr := resp.Error().(*StimApiError)
    if stimErr.Message != "" {
      return diag.FromErr(stimErr)
    }
    return append(diags, diag.Diagnostic{
      Severity: diag.Error,
      Summary:  "Unable to create cluster",
      Detail:   "Unknown error",
    })
	}
	json := resp.Result().(*PostClusterPayload)
	d.SetId(json.Name)

  err = resource.RetryContext(ctx, d.Timeout(schema.TimeoutCreate), func() *resource.RetryError {
    resp, err := client.describeCluster(json.Name)

    if err != nil {
      return resource.NonRetryableError(fmt.Errorf("Error describing cluster: %s", err))
    }

    if resp.State != "ACTIVE" {
      return resource.RetryableError(fmt.Errorf("Expected instance to be active but was in state %s", resp.State))
    }

    return nil
  })
  if err != nil {
    return diag.FromErr(err)
  }

	return diags
}

func resourceClusterUpdate(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
  log.Printf("[DEBUG] Updating cluster: %s", d.Id())
	var diags diag.Diagnostics
	client := meta.(*StimClient).stimApi
  if d.HasChange("nodes") {
    newNodes := d.Get("nodes").([]interface{})
    for _, item := range newNodes {
      newNode := item.(map[string]interface{})
      input := PutNodePoolByTypeInput{
        Quantity: newNode["quantity"].(int),
      }
      resp, err := client.R().SetError(StimApiError{}).SetBody(input).ForceContentType("application/json").Put(fmt.Sprintf("rest/v1/cluster/%v/node-pool/type/%s", d.Id(), newNode["node_type"]))
      if err != nil {
        return diag.FromErr(err)
      }
      if resp.StatusCode() != 200 {
        stimErr := resp.Error().(*StimApiError)
        if stimErr.Message != "" {
          return diag.FromErr(stimErr)
        }
        return append(diags, diag.Diagnostic{
          Severity: diag.Error,
          Summary:  "Unable to update node pool",
          Detail:   "Unknown error",
        })
      }
    }
  }
  return diags
}

func resourceClusterDelete(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
  log.Printf("[DEBUG] Deleting cluster: %s", d.Id())
	client := meta.(*StimClient)
	api := client.stimApi
	resp, err := api.R().SetError(StimApiError{}).ForceContentType("application/json").Delete(fmt.Sprintf("rest/v1/cluster/%v", d.Id()))
	if err != nil {
		return diag.FromErr(err)
	}
	if resp.StatusCode() != 200 {
		stimErr := resp.Error().(*StimApiError)
		if stimErr.Message != "" {
			return diag.FromErr(stimErr)
		}
		return diag.FromErr(err)
	}
	var diags diag.Diagnostics
  err = resource.RetryContext(ctx, d.Timeout(schema.TimeoutDelete), func() *resource.RetryError {
    resp, err := client.describeCluster(d.Id())

    if err != nil {
      return resource.NonRetryableError(fmt.Errorf("Error describing cluster: %s", err))
    }

    if resp != nil {
      return resource.RetryableError(fmt.Errorf("Expected cluster to get removed but cluster is still returned from api"))
    }

    return nil
  })
  if err != nil {
    return diag.FromErr(err)
  }

	return diags
}

func resourceClusterRead(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
  log.Printf("[DEBUG] Reading cluster: %s", d.Id())
	client := meta.(*StimClient)
	cluster, err := client.describeCluster(d.Id())
	if err != nil {
		return diag.FromErr(err)
	}
	d.Set("name", cluster.Name)
	d.Set("state", cluster.State)
	d.Set("nodes", cluster.NodePools)

	var diags diag.Diagnostics
	return diags
}
