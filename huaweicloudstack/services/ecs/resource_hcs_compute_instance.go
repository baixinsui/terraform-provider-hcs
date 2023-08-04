package ecs

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/resource"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/validation"

	"github.com/huaweicloud/terraform-provider-hcs/huaweicloudstack/common"
	"github.com/huaweicloud/terraform-provider-hcs/huaweicloudstack/config"
	"github.com/huaweicloud/terraform-provider-hcs/huaweicloudstack/helper/hashcode"
	golangsdk "github.com/huaweicloud/terraform-provider-hcs/huaweicloudstack/sdk/huaweicloud"
	"github.com/huaweicloud/terraform-provider-hcs/huaweicloudstack/sdk/huaweicloud/openstack/compute/v2/extensions/bootfromvolume"
	"github.com/huaweicloud/terraform-provider-hcs/huaweicloudstack/sdk/huaweicloud/openstack/compute/v2/extensions/keypairs"
	"github.com/huaweicloud/terraform-provider-hcs/huaweicloudstack/sdk/huaweicloud/openstack/compute/v2/extensions/schedulerhints"
	"github.com/huaweicloud/terraform-provider-hcs/huaweicloudstack/sdk/huaweicloud/openstack/compute/v2/extensions/secgroups"
	"github.com/huaweicloud/terraform-provider-hcs/huaweicloudstack/sdk/huaweicloud/openstack/compute/v2/servers"
	"github.com/huaweicloud/terraform-provider-hcs/huaweicloudstack/sdk/huaweicloud/openstack/ecs/v1/block_devices"
	"github.com/huaweicloud/terraform-provider-hcs/huaweicloudstack/sdk/huaweicloud/openstack/ecs/v1/cloudservers"
	"github.com/huaweicloud/terraform-provider-hcs/huaweicloudstack/sdk/huaweicloud/openstack/ecs/v1/powers"
	"github.com/huaweicloud/terraform-provider-hcs/huaweicloudstack/sdk/huaweicloud/openstack/evs/v2/cloudvolumes"
	"github.com/huaweicloud/terraform-provider-hcs/huaweicloudstack/sdk/huaweicloud/openstack/ims/v2/cloudimages"
	groups "github.com/huaweicloud/terraform-provider-hcs/huaweicloudstack/sdk/huaweicloud/openstack/networking/v1/security/securitygroups"
	"github.com/huaweicloud/terraform-provider-hcs/huaweicloudstack/sdk/huaweicloud/openstack/networking/v1/subnets"
	"github.com/huaweicloud/terraform-provider-hcs/huaweicloudstack/sdk/huaweicloud/openstack/networking/v2/ports"
	"github.com/huaweicloud/terraform-provider-hcs/huaweicloudstack/services/evs"
	"github.com/huaweicloud/terraform-provider-hcs/huaweicloudstack/utils"
)

var (
	novaConflicts  = []string{"block_device_mapping_v2", "metadata"}
	powerActionMap = map[string]string{
		"ON":     "os-start",
		"OFF":    "os-stop",
		"REBOOT": "reboot",
	}
)

func ResourceComputeInstance() *schema.Resource {
	return &schema.Resource{
		CreateContext: resourceComputeInstanceCreate,
		ReadContext:   resourceComputeInstanceRead,
		UpdateContext: resourceComputeInstanceUpdate,
		DeleteContext: resourceComputeInstanceDelete,

		Importer: &schema.ResourceImporter{
			StateContext: resourceComputeInstanceImportState,
		},

		Timeouts: &schema.ResourceTimeout{
			Create: schema.DefaultTimeout(30 * time.Minute),
			Update: schema.DefaultTimeout(30 * time.Minute),
			Delete: schema.DefaultTimeout(30 * time.Minute),
		},

		Schema: map[string]*schema.Schema{
			"region": {
				Type:     schema.TypeString,
				Optional: true,
				Computed: true,
				ForceNew: true,
			},
			"availability_zone": {
				Type:     schema.TypeString,
				Optional: true,
				Computed: true,
				ForceNew: true,
			},
			"name": {
				Type:     schema.TypeString,
				Required: true,
			},
			"description": {
				Type:     schema.TypeString,
				Optional: true,
				Computed: true,
			},
			"image_id": {
				Type:        schema.TypeString,
				Optional:    true,
				ForceNew:    true,
				Computed:    true,
				DefaultFunc: schema.EnvDefaultFunc("HW_IMAGE_ID", nil),
			},
			"image_name": {
				Type:        schema.TypeString,
				Optional:    true,
				ForceNew:    true,
				Computed:    true,
				DefaultFunc: schema.EnvDefaultFunc("HW_IMAGE_NAME", nil),
			},
			"flavor_id": {
				Type:        schema.TypeString,
				Optional:    true,
				Computed:    true,
				DefaultFunc: schema.EnvDefaultFunc("HW_FLAVOR_ID", nil),
				Description: "schema: Required",
			},
			"flavor_name": {
				Type:        schema.TypeString,
				Optional:    true,
				Computed:    true,
				DefaultFunc: schema.EnvDefaultFunc("HW_FLAVOR_NAME", nil),
				Description: "schema: Computed",
			},
			"admin_pass": {
				Type:      schema.TypeString,
				Sensitive: true,
				Optional:  true,
			},
			"key_pair": {
				Type:     schema.TypeString,
				Optional: true,
			},
			"private_key": {
				Type:      schema.TypeString,
				Optional:  true,
				Sensitive: true,
			},
			"security_groups": {
				Type:          schema.TypeSet,
				Optional:      true,
				Computed:      true,
				Description:   "schema: Computed",
				ConflictsWith: []string{"security_group_ids"},
				Elem:          &schema.Schema{Type: schema.TypeString},
				Set:           schema.HashString,
			},
			"security_group_ids": {
				Type:     schema.TypeSet,
				Optional: true,
				Computed: true,
				Elem:     &schema.Schema{Type: schema.TypeString},
				Set:      schema.HashString,
			},
			"network": {
				Type:     schema.TypeList,
				Required: true,
				ForceNew: true,
				MaxItems: 12,
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"uuid": {
							Type:        schema.TypeString,
							Optional:    true,
							ForceNew:    true,
							Computed:    true,
							Description: "schema: Required",
						},
						"port": {
							Type:        schema.TypeString,
							Optional:    true,
							ForceNew:    true,
							Computed:    true,
							Description: "schema: Computed",
						},
						"ipv6_enable": {
							Type:     schema.TypeBool,
							Optional: true,
							ForceNew: true,
						},
						"fixed_ip_v4": {
							Type:     schema.TypeString,
							Optional: true,
							ForceNew: true,
							Computed: true,
						},
						"source_dest_check": {
							Type:     schema.TypeBool,
							Optional: true,
							Default:  true,
						},
						"fixed_ip_v6": {
							Type:        schema.TypeString,
							Optional:    true,
							ForceNew:    true,
							Computed:    true,
							Description: "schema: Computed",
						},
						"mac": {
							Type:     schema.TypeString,
							Computed: true,
						},
						"access_network": {
							Type:     schema.TypeBool,
							Optional: true,
							Default:  false,
						},
					},
				},
			},
			"system_disk_type": {
				Type:          schema.TypeString,
				Optional:      true,
				ForceNew:      true,
				Computed:      true,
				ConflictsWith: novaConflicts,
			},
			"system_disk_size": {
				Type:          schema.TypeInt,
				Optional:      true,
				Computed:      true,
				ConflictsWith: novaConflicts,
			},
			"data_disks": {
				Type:          schema.TypeList,
				Optional:      true,
				ForceNew:      true,
				ConflictsWith: novaConflicts,
				MaxItems:      23,
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"type": {
							Type:     schema.TypeString,
							Required: true,
							ForceNew: true,
						},
						"size": {
							Type:     schema.TypeInt,
							Required: true,
							ForceNew: true,
						},
						"snapshot_id": {
							Type:     schema.TypeString,
							Optional: true,
							ForceNew: true,
						},
						"kms_key_id": {
							Type:     schema.TypeString,
							Optional: true,
							ForceNew: true,
						},
					},
				},
			},
			"scheduler_hints": {
				Type:     schema.TypeSet,
				Optional: true,
				Computed: true,
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"group": {
							Type:     schema.TypeString,
							Optional: true,
							Computed: true,
							ForceNew: true,
						},
						"fault_domain": {
							Type:        schema.TypeString,
							Optional:    true,
							ForceNew:    true,
							Description: "schema: Internal",
						},
						"tenancy": {
							Type:     schema.TypeString,
							Optional: true,
							ForceNew: true,
						},
						"deh_id": {
							Type:     schema.TypeString,
							Optional: true,
							ForceNew: true,
						},
					},
				},
				Set: resourceComputeSchedulerHintsHash,
			},
			"user_data": {
				Type:     schema.TypeString,
				Optional: true,
				ForceNew: true,
				// just stash the hash for state & diff comparisons
				StateFunc: utils.HashAndHexEncode,
			},
			"stop_before_destroy": {
				Type:     schema.TypeBool,
				Optional: true,
				Default:  false,
			},
			"delete_disks_on_termination": {
				Type:          schema.TypeBool,
				Optional:      true,
				ConflictsWith: novaConflicts,
			},
			"delete_eip_on_termination": {
				Type:          schema.TypeBool,
				Optional:      true,
				Default:       true,
				ConflictsWith: novaConflicts,
			},

			"eip_id": {
				Type:          schema.TypeString,
				Optional:      true,
				ForceNew:      true,
				ConflictsWith: []string{"eip_type", "bandwidth"},
			},
			"eip_type": {
				Type:          schema.TypeString,
				Optional:      true,
				ForceNew:      true,
				ConflictsWith: []string{"eip_id"},
				RequiredWith:  []string{"bandwidth"},
			},
			"bandwidth": {
				Type:          schema.TypeList,
				Optional:      true,
				ForceNew:      true,
				MaxItems:      1,
				ConflictsWith: []string{"eip_id"},
				RequiredWith:  []string{"eip_type"},
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"share_type": {
							Type:     schema.TypeString,
							Required: true,
							ForceNew: true,
							ValidateFunc: validation.StringInSlice([]string{
								"PER", "WHOLE",
							}, true),
						},
						"id": {
							Type:          schema.TypeString,
							Optional:      true,
							ForceNew:      true,
							ConflictsWith: []string{"bandwidth.0.size", "bandwidth.0.charge_mode"},
						},
						"size": {
							Type:         schema.TypeInt,
							Optional:     true,
							ForceNew:     true,
							RequiredWith: []string{"bandwidth.0.charge_mode"},
						},
						"charge_mode": {
							Type:         schema.TypeString,
							Optional:     true,
							ForceNew:     true,
							RequiredWith: []string{"bandwidth.0.size"},
						},
					},
				},
			},

			// charge info: charging_mode, period_unit, period, auto_renew, auto_pay
			"charging_mode": {
				Type:     schema.TypeString,
				Optional: true,
				ForceNew: true,
				Computed: true,
				ValidateFunc: validation.StringInSlice([]string{
					"prePaid", "postPaid", "spot",
				}, false),
				ConflictsWith: novaConflicts,
			},
			"period_unit": common.SchemaPeriodUnit(novaConflicts),
			"period":      common.SchemaPeriod(novaConflicts),
			"auto_renew":  common.SchemaAutoRenewUpdatable(novaConflicts),
			"auto_pay":    common.SchemaAutoPay(novaConflicts),

			"spot_maximum_price": {
				Type:          schema.TypeString,
				Optional:      true,
				ForceNew:      true,
				ConflictsWith: []string{"spot_duration", "spot_duration_count"},
			},
			"spot_duration": {
				Type:         schema.TypeInt,
				Optional:     true,
				ForceNew:     true,
				ValidateFunc: validation.IntBetween(1, 6),
			},
			"spot_duration_count": {
				Type:         schema.TypeInt,
				Optional:     true,
				Computed:     true,
				ForceNew:     true,
				RequiredWith: []string{"spot_duration"},
			},

			"user_id": { // required if in prePaid charging mode with key_pair.
				Type:     schema.TypeString,
				Optional: true,
				ForceNew: true,
			},
			"agency_name": {
				Type:     schema.TypeString,
				Optional: true,
				Computed: true,
			},
			"agent_list": {
				Type:     schema.TypeString,
				Optional: true,
				Computed: true,
			},
			"tags": {
				Type:     schema.TypeMap,
				Optional: true,
				Elem:     &schema.Schema{Type: schema.TypeString},
			},
			"power_action": {
				Type:     schema.TypeString,
				Optional: true,
				Computed: true,
				// If you want to support more actions, please update powerActionMap simultaneously.
				ValidateFunc: validation.StringInSlice([]string{
					"ON", "OFF", "REBOOT", "FORCE-OFF", "FORCE-REBOOT",
				}, false),
			},
			"volume_attached": {
				Type:     schema.TypeList,
				Computed: true,
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"volume_id": {
							Type:     schema.TypeString,
							Computed: true,
						},
						"boot_index": {
							Type:     schema.TypeInt,
							Computed: true,
						},
						"size": {
							Type:     schema.TypeInt,
							Computed: true,
						},
						"type": {
							Type:     schema.TypeString,
							Computed: true,
						},
						"pci_address": {
							Type:     schema.TypeString,
							Computed: true,
						},
						"kms_key_id": {
							Type:     schema.TypeString,
							Computed: true,
						},
					},
				},
			},
			"system_disk_id": {
				Type:     schema.TypeString,
				Computed: true,
			},
			"public_ip": {
				Type:     schema.TypeString,
				Computed: true,
			},
			"access_ip_v4": {
				Type:     schema.TypeString,
				Computed: true,
			},
			"access_ip_v6": {
				Type:     schema.TypeString,
				Computed: true,
			},
			"status": {
				Type:     schema.TypeString,
				Computed: true,
			},
			"created_at": {
				Type:     schema.TypeString,
				Computed: true,
			},
			"updated_at": {
				Type:     schema.TypeString,
				Computed: true,
			},

			// Deprecated
			"metadata": {
				Type:          schema.TypeMap,
				Optional:      true,
				ConflictsWith: []string{"system_disk_type", "system_disk_size", "data_disks"},
				Deprecated:    "use tags instead",
				Elem:          &schema.Schema{Type: schema.TypeString},
			},
			"block_device_mapping_v2": {
				Type:          schema.TypeList,
				Optional:      true,
				ConflictsWith: []string{"system_disk_type", "system_disk_size", "data_disks"},
				Deprecated:    "use system_disk_type, system_disk_size, data_disks instead",
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"source_type": {
							Type:     schema.TypeString,
							Optional: true,
						},
						"uuid": {
							Type:     schema.TypeString,
							Optional: true,
							ForceNew: true,
						},
						"volume_size": {
							Type:     schema.TypeInt,
							Optional: true,
							ForceNew: true,
						},
						"volume_type": {
							Type:     schema.TypeString,
							Optional: true,
							ForceNew: true,
						},
						"destination_type": {
							Type:     schema.TypeString,
							Optional: true,
						},
						"boot_index": {
							Type:     schema.TypeInt,
							Optional: true,
							ForceNew: true,
						},
						"delete_on_termination": {
							Type:     schema.TypeBool,
							Optional: true,
							Default:  false,
							ForceNew: true,
						},
					},
				},
			},
		},
	}
}

func hasDeprecatedConfig(d *schema.ResourceData) bool {
	if _, ok := d.GetOk("metadata"); ok {
		return true
	}
	if _, ok := d.GetOk("block_device_mapping_v2"); ok {
		return true
	}
	return false
}

func getSpotDurationCount(d *schema.ResourceData) int {
	var count = 1
	if c, ok := d.GetOk("spot_duration_count"); ok {
		count = c.(int)
	}
	return count
}

func resourceComputeInstanceCreate(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
	cfg := meta.(*config.Config)
	region := cfg.GetRegion(d)
	computeClient, err := cfg.ComputeV2Client(region)
	if err != nil {
		return diag.Errorf("error creating compute V2 client: %s", err)
	}
	ecsClient, err := cfg.ComputeV1Client(region)
	if err != nil {
		return diag.Errorf("error creating compute V1 client: %s", err)
	}
	imsClient, err := cfg.ImageV2Client(region)
	if err != nil {
		return diag.Errorf("error creating image client: %s", err)
	}
	nicClient, err := cfg.NetworkingV2Client(region)
	if err != nil {
		return diag.Errorf("error creating networking client: %s", err)
	}

	if err := validateComputeInstanceConfig(d, cfg); err != nil {
		return diag.FromErr(err)
	}

	// Determines the Image ID using the following rules:
	// If a bootable block_device_mapping_v2 was specified, ignore the image altogether.
	// If an image_id was specified, use it.
	// If an image_name was specified, look up the image ID, report if error.
	imageId, err := getImageIDFromConfig(imsClient, d)
	if err != nil {
		return diag.FromErr(err)
	}

	flavorId, err := getFlavorID(d)
	if err != nil {
		return diag.FromErr(err)
	}

	// determine if block_device_mapping_v2 configuration is correct
	// this includes valid combinations and required attributes
	if err := checkBlockDeviceConfig(d); err != nil {
		return diag.FromErr(err)
	}

	// Try to call API of Huawei ECS instead of OpenStack which is deprecated
	if !hasDeprecatedConfig(d) {
		ecsV11Client, err := cfg.ComputeV11Client(region)
		if err != nil {
			return diag.Errorf("error creating compute V1.1 client: %s", err)
		}
		vpcClient, err := cfg.NetworkingV1Client(region)
		if err != nil {
			return diag.Errorf("error creating networking V1 client: %s", err)
		}

		vpcId, err := getVpcID(vpcClient, d)
		if err != nil {
			return diag.FromErr(err)
		}

		secGroups, err := resourceInstanceSecGroupIdsV1(vpcClient, d)
		if err != nil {
			return diag.FromErr(err)
		}

		createOpts := &cloudservers.CreateOpts{
			Name:             d.Get("name").(string),
			Description:      d.Get("description").(string),
			ImageRef:         imageId,
			FlavorRef:        flavorId,
			KeyName:          d.Get("key_pair").(string),
			VpcId:            vpcId,
			SecurityGroups:   secGroups,
			AvailabilityZone: d.Get("availability_zone").(string),
			RootVolume:       resourceInstanceRootVolume(d),
			DataVolumes:      resourceInstanceDataVolumes(d),
			Nics:             buildInstanceNicsRequest(d),
			PublicIp:         buildInstancePublicIPRequest(d),
			UserData:         []byte(d.Get("user_data").(string)),
		}

		if tags, ok := d.GetOk("tags"); ok {
			createOpts.ServerTags = utils.ExpandResourceTags(tags.(map[string]interface{}))
		}

		var extendParam cloudservers.ServerExtendParam
		chargingMode := d.Get("charging_mode").(string)
		if chargingMode == "prePaid" {
			if err := common.ValidatePrePaidChargeInfo(d); err != nil {
				return diag.FromErr(err)
			}

			extendParam.ChargingMode = chargingMode
			extendParam.PeriodType = d.Get("period_unit").(string)
			extendParam.PeriodNum = d.Get("period").(int)
			extendParam.IsAutoRenew = d.Get("auto_renew").(string)
			extendParam.IsAutoPay = common.GetAutoPay(d)
		} else if chargingMode == "spot" {
			extendParam.MarketType = "spot"
			extendParam.SpotPrice = d.Get("spot_maximum_price").(string)
			if v, ok := d.GetOk("spot_duration"); ok {
				extendParam.InterruptionPolicy = "immediate"
				extendParam.SpotDurationHours = v.(int)
				extendParam.SpotDurationCount = getSpotDurationCount(d)
			}
		}

		if extendParam != (cloudservers.ServerExtendParam{}) {
			createOpts.ExtendParam = &extendParam
		}

		var metadata cloudservers.MetaData
		metadata.OpSvcUserId = getOpSvcUserID(d, cfg)

		if v, ok := d.GetOk("agency_name"); ok {
			metadata.AgencyName = v.(string)
		}
		if v, ok := d.GetOk("agent_list"); ok {
			metadata.AgentList = v.(string)
		}
		if metadata != (cloudservers.MetaData{}) {
			createOpts.MetaData = &metadata
		}

		schedulerHintsRaw := d.Get("scheduler_hints").(*schema.Set).List()
		if len(schedulerHintsRaw) > 0 {
			log.Printf("[DEBUG] schedulerhints: %+v", schedulerHintsRaw)
			schedulerHints := resourceInstanceSchedulerHintsV1(schedulerHintsRaw[0].(map[string]interface{}))
			createOpts.SchedulerHints = &schedulerHints
		}

		log.Printf("[DEBUG] ECS create options: %#v", createOpts)
		// Add password here so it wouldn't go in the above log entry
		createOpts.AdminPass = d.Get("admin_pass").(string)

		if d.Get("charging_mode") == "prePaid" {
			// prePaid.
			n, err := cloudservers.CreatePrePaid(ecsV11Client, createOpts).ExtractOrderResponse()
			if err != nil {
				return diag.Errorf("error creating server: %s", err)
			}
			bssClient, err := cfg.BssV2Client(region)
			if err != nil {
				return diag.Errorf("error creating BSS v2 client: %s", err)
			}
			err = common.WaitOrderComplete(ctx, bssClient, n.OrderID, d.Timeout(schema.TimeoutCreate))
			if err != nil {
				return diag.FromErr(err)
			}
			resourceId, err := common.WaitOrderResourceComplete(ctx, bssClient, n.OrderID, d.Timeout(schema.TimeoutCreate))
			if err != nil {
				return diag.FromErr(err)
			}
			d.SetId(resourceId)
		} else {
			// postPaid.
			n, err := cloudservers.Create(ecsV11Client, createOpts).ExtractJobResponse()
			if err != nil {
				return diag.Errorf("error creating server: %s", err)
			}
			if err := cloudservers.WaitForJobSuccess(ecsClient, int(d.Timeout(schema.TimeoutCreate)/time.Second), n.JobID); err != nil {
				return diag.FromErr(err)
			}
			serverId, err := cloudservers.GetJobEntity(ecsClient, n.JobID, "server_id")
			if err != nil {
				return diag.FromErr(err)
			}
			d.SetId(serverId.(string))
		}
	} else {
		// OpenStack API implementation. Clean up this after removing block_device.

		// Build a []servers.Network to pass into the create options.
		networks, err := expandInstanceNetworks(d)
		if err != nil {
			return diag.FromErr(err)
		}

		var createOpts servers.CreateOptsBuilder
		createOpts = &servers.CreateOpts{
			Name:             d.Get("name").(string),
			ImageRef:         imageId,
			FlavorRef:        flavorId,
			SecurityGroups:   resourceInstanceSecGroupsV2(d),
			AvailabilityZone: d.Get("availability_zone").(string),
			Networks:         networks,
			Metadata:         resourceInstanceMetadataV2(d),
			AdminPass:        d.Get("admin_pass").(string),
			UserData:         []byte(d.Get("user_data").(string)),
		}

		if keyName, ok := d.Get("key_pair").(string); ok && keyName != "" {
			createOpts = &keypairs.CreateOptsExt{
				CreateOptsBuilder: createOpts,
				KeyName:           keyName,
			}
		}

		if vL, ok := d.GetOk("block_device_mapping_v2"); ok {
			blockDevices, err := resourceInstanceBlockDevicesV2(vL.([]interface{}))
			if err != nil {
				return diag.FromErr(err)
			}

			createOpts = &bootfromvolume.CreateOptsExt{
				CreateOptsBuilder: createOpts,
				BlockDevice:       blockDevices,
			}
		}

		schedulerHintsRaw := d.Get("scheduler_hints").(*schema.Set).List()
		if len(schedulerHintsRaw) > 0 {
			log.Printf("[DEBUG] schedulerhints: %+v", schedulerHintsRaw)
			schedulerHints := resourceInstanceSchedulerHintsV2(schedulerHintsRaw[0].(map[string]interface{}))
			createOpts = &schedulerhints.CreateOptsExt{
				CreateOptsBuilder: createOpts,
				SchedulerHints:    schedulerHints,
			}
		}

		log.Printf("[DEBUG] compute create options: %#v", createOpts)

		// If a block_device_mapping_v2  is used, use the bootfromvolume.Create function as it allows an empty ImageRef.
		// Otherwise, use the normal servers.Create function.
		var server *servers.Server
		server, err = servers.Create(computeClient, createOpts).Extract()

		if err != nil {
			return diag.Errorf("error creating server: %s", err)
		}

		log.Printf("[INFO] instance ID: %s", server.ID)

		// Store the ID now
		d.SetId(server.ID)

		// Wait for the instance to become running so we can get some attributes
		// that aren't available until later.
		log.Printf("[DEBUG] waiting for instance (%s) to become running", server.ID)
		pending := []string{"BUILD"}
		target := []string{"ACTIVE"}
		timeout := d.Timeout(schema.TimeoutCreate)
		if err := waitForServerTargetState(ctx, ecsClient, d.Id(), pending, target, timeout); err != nil {
			return diag.Errorf("State waiting timeout: %s", err)
		}
	}

	// Create an instance in the shutdown state.
	if action, ok := d.GetOk("power_action"); ok {
		action := action.(string)
		if action == "OFF" || action == "FORCE-OFF" {
			if err = doPowerAction(ecsClient, d, action); err != nil {
				return diag.Errorf("Doing power action (%s) for instance (%s) failed: %s", action, d.Id(), err)
			}
		} else {
			log.Printf("[WARN] the power action (%s) is invalid after instance created", action)
		}
	}

	// get the original value of source_dest_check in script
	originalNetworks := d.Get("network").([]interface{})
	sourceDestChecks := make([]bool, len(originalNetworks))
	var flag bool

	for i, v := range originalNetworks {
		nic := v.(map[string]interface{})
		sourceDestChecks[i] = nic["source_dest_check"].(bool)
		if !flag && !sourceDestChecks[i] {
			flag = true
		}
	}

	if flag {
		// Get the instance network and address information
		server, err := cloudservers.Get(ecsClient, d.Id()).Extract()
		if err != nil {
			return diag.Errorf("error retrieving compute instance: %s", d.Id())
		}
		networks, err := flattenInstanceNetworks(d, meta, server)
		if err != nil {
			return diag.FromErr(err)
		}

		for i, nic := range networks {
			nicPort := nic["port"].(string)
			if nicPort == "" {
				continue
			}

			if !sourceDestChecks[i] {
				if err := disableSourceDestCheck(nicClient, nicPort); err != nil {
					return diag.Errorf("error disabling source dest check on port(%s) of instance(%s): %s", nicPort, d.Id(), err)
				}
			}
		}
	}

	return resourceComputeInstanceRead(ctx, d, meta)
}

func resourceComputeInstanceRead(_ context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
	cfg := meta.(*config.Config)
	region := cfg.GetRegion(d)
	ecsClient, err := cfg.ComputeV1Client(region)
	if err != nil {
		return diag.Errorf("error creating compute V1 client: %s", err)
	}
	blockStorageClient, err := cfg.BlockStorageV2Client(region)
	if err != nil {
		return diag.Errorf("error creating evs client: %s", err)
	}
	imsClient, err := cfg.ImageV2Client(region)
	if err != nil {
		return diag.Errorf("error creating image client: %s", err)
	}

	server, err := cloudservers.Get(ecsClient, d.Id()).Extract()
	if err != nil {
		return common.CheckDeletedDiag(d, err, "error retrieving compute instance")
	} else if server.Status == "DELETED" {
		d.SetId("")
		return nil
	}

	log.Printf("[DEBUG] retrieved compute instance %s: %+v", d.Id(), server)
	// Set some attributes
	d.Set("region", region)
	d.Set("availability_zone", server.AvailabilityZone)
	d.Set("name", server.Name)
	d.Set("description", server.Description)
	d.Set("status", server.Status)
	d.Set("agency_name", server.Metadata.AgencyName)
	d.Set("agent_list", server.Metadata.AgentList)
	d.Set("charging_mode", normalizeChargingMode(server.Metadata.ChargingMode))
	d.Set("created_at", server.Created.Format(time.RFC3339))
	d.Set("updated_at", server.Updated.Format(time.RFC3339))

	flavorInfo := server.Flavor
	d.Set("flavor_id", flavorInfo.ID)
	d.Set("flavor_name", flavorInfo.Name)

	// Set the instance's image information appropriately
	if err := setImageInformation(d, imsClient, server.Image.ID); err != nil {
		return diag.FromErr(err)
	}

	if server.KeyName != "" {
		d.Set("key_pair", server.KeyName)
	}
	if eip := computePublicIP(server); eip != "" {
		d.Set("public_ip", eip)
	}

	// Get the instance network and address information
	networks, err := flattenInstanceNetworks(d, meta, server)
	if err != nil {
		return diag.FromErr(err)
	}
	// Determine the best IPv4 and IPv6 addresses to access the instance with
	hostv4, hostv6 := getInstanceAccessAddresses(networks)

	// update hostv4/6 by AccessIPv4/v6
	// currently, AccessIPv4/v6 are Reserved in HuaweiCloud
	if server.AccessIPv4 != "" {
		hostv4 = server.AccessIPv4
	}
	if server.AccessIPv6 != "" {
		hostv6 = server.AccessIPv6
	}

	d.Set("network", networks)
	d.Set("access_ip_v4", hostv4)
	d.Set("access_ip_v6", hostv6)

	// Determine the best IP address to use for SSH connectivity.
	// Prefer IPv4 over IPv6.
	var preferredSSHAddress string
	if hostv4 != "" {
		preferredSSHAddress = hostv4
	} else if hostv6 != "" {
		preferredSSHAddress = hostv6
	}

	if preferredSSHAddress != "" {
		// Initialize the connection info
		d.SetConnInfo(map[string]string{
			"type": "ssh",
			"host": preferredSSHAddress,
		})
	}

	secGrpNames := []string{}
	for _, sg := range server.SecurityGroups {
		secGrpNames = append(secGrpNames, sg.Name)
	}
	d.Set("security_groups", secGrpNames)

	secGrpIDs := make([]string, len(server.SecurityGroups))
	for i, sg := range server.SecurityGroups {
		secGrpIDs[i] = sg.ID
	}
	d.Set("security_group_ids", secGrpIDs)

	// Set volume attached
	if len(server.VolumeAttached) > 0 {
		bds := make([]map[string]interface{}, len(server.VolumeAttached))
		for i, b := range server.VolumeAttached {
			// retrieve volume `size` and `type`
			volumeInfo, err := cloudvolumes.Get(blockStorageClient, b.ID).Extract()
			if err != nil {
				return diag.FromErr(err)
			}
			log.Printf("[DEBUG] retrieved volume %s: %#v", b.ID, volumeInfo)

			// retrieve volume `pci_address`
			va, err := block_devices.Get(ecsClient, d.Id(), b.ID).Extract()
			if err != nil {
				return diag.FromErr(err)
			}
			log.Printf("[DEBUG] retrieved block device %s: %#v", b.ID, va)

			bds[i] = map[string]interface{}{
				"volume_id":   b.ID,
				"size":        volumeInfo.Size,
				"type":        volumeInfo.VolumeType,
				"boot_index":  va.BootIndex,
				"pci_address": va.PciAddress,
				"kms_key_id":  volumeInfo.Metadata.SystemCmkID,
			}

			if va.BootIndex == 0 {
				d.Set("system_disk_id", b.ID)
				d.Set("system_disk_size", volumeInfo.Size)
				d.Set("system_disk_type", volumeInfo.VolumeType)
			}
		}
		d.Set("volume_attached", bds)
	}

	// set scheduler_hints
	osHints := server.OsSchedulerHints
	if len(osHints.Group) > 0 {
		schedulerHints := make([]map[string]interface{}, len(osHints.Group))
		for i, v := range osHints.Group {
			schedulerHints[i] = map[string]interface{}{
				"group": v,
			}
		}
		d.Set("scheduler_hints", schedulerHints)
	}

	// Set instance tags
	d.Set("tags", flattenTagsToMap(server.Tags))

	return nil
}

func normalizeChargingMode(mode string) string {
	var ret string
	switch mode {
	case "1":
		ret = "prePaid"
	case "2":
		ret = "spot"
	default:
		ret = "postPaid"
	}

	return ret
}

func resourceComputeInstanceUpdate(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
	cfg := meta.(*config.Config)
	region := cfg.GetRegion(d)
	computeClient, err := cfg.ComputeV2Client(region)
	if err != nil {
		return diag.Errorf("error creating compute V2 client: %s", err)
	}
	ecsClient, err := cfg.ComputeV1Client(region)
	if err != nil {
		return diag.Errorf("error creating compute V1 client: %s", err)
	}
	ecsV11Client, err := cfg.ComputeV11Client(region)
	if err != nil {
		return diag.Errorf("error creating compute V1.1 client: %s", err)
	}

	if d.HasChanges("name", "description") {
		var updateOpts cloudservers.UpdateOpts
		updateOpts.Name = d.Get("name").(string)
		description := d.Get("description").(string)
		updateOpts.Description = &description

		err := cloudservers.Update(ecsClient, d.Id(), updateOpts).ExtractErr()
		if err != nil {
			return diag.Errorf("error updating server: %s", err)
		}
	}

	if d.HasChange("metadata") {
		oldMetadata, newMetadata := d.GetChange("metadata")
		var metadataToDelete []string

		// Determine if any metadata keys were removed from the configuration.
		// Then request those keys to be deleted.
		for oldKey := range oldMetadata.(map[string]interface{}) {
			var found bool
			for newKey := range newMetadata.(map[string]interface{}) {
				if oldKey == newKey {
					found = true
				}
			}

			if !found {
				metadataToDelete = append(metadataToDelete, oldKey)
			}
		}

		for _, key := range metadataToDelete {
			err := servers.DeleteMetadatum(computeClient, d.Id(), key).ExtractErr()
			if err != nil {
				return diag.Errorf("error deleting metadata (%s) from server (%s): %s", key, d.Id(), err)
			}
		}

		// Update existing metadata and add any new metadata.
		metadataOpts := make(servers.MetadataOpts)
		for k, v := range newMetadata.(map[string]interface{}) {
			metadataOpts[k] = v.(string)
		}

		_, err := servers.UpdateMetadata(computeClient, d.Id(), metadataOpts).Extract()
		if err != nil {
			return diag.Errorf("error updating server (%s) metadata: %s", d.Id(), err)
		}
	}

	if d.HasChanges("agency_name", "agent_list ") {
		metadataOpts := make(servers.MetadataOpts)
		if d.HasChange("agency_name") {
			metadataOpts["agency_name"] = d.Get("agency_name").(string)
		}
		if d.HasChange("agent_list") {
			metadataOpts["__support_agent_list"] = d.Get("agent_list").(string)
		}
		_, err = servers.UpdateMetadata(computeClient, d.Id(), metadataOpts).Extract()
		if err != nil {
			return diag.Errorf("error updating server (%s) metadata(agency_name, agent_list) : %s", d.Id(), err)
		}
	}

	if d.HasChanges("security_group_ids", "security_groups") {
		var oldSGRaw interface{}
		var newSGRaw interface{}
		if d.HasChange("security_group_ids") {
			oldSGRaw, newSGRaw = d.GetChange("security_group_ids")
		} else {
			oldSGRaw, newSGRaw = d.GetChange("security_groups")
		}
		oldSGSet := oldSGRaw.(*schema.Set)
		newSGSet := newSGRaw.(*schema.Set)
		secgroupsToAdd := newSGSet.Difference(oldSGSet)
		secgroupsToRemove := oldSGSet.Difference(newSGSet)
		log.Printf("[DEBUG] security groups to add: %v", secgroupsToAdd)
		log.Printf("[DEBUG] security groups to remove: %v", secgroupsToRemove)

		for _, g := range secgroupsToRemove.List() {
			err := secgroups.RemoveServer(computeClient, d.Id(), g.(string)).ExtractErr()
			if err != nil && err.Error() != "EOF" {
				if _, ok := err.(golangsdk.ErrDefault404); ok {
					continue
				}
				return diag.Errorf("error removing security group (%s) from server (%s): %s", g, d.Id(), err)
			}
			log.Printf("[DEBUG] removed security group (%s) from instance (%s)", g, d.Id())
		}

		for _, g := range secgroupsToAdd.List() {
			err := secgroups.AddServer(computeClient, d.Id(), g.(string)).ExtractErr()
			if err != nil && err.Error() != "EOF" {
				return diag.Errorf("error adding security group (%s) to server (%s): %s", g, d.Id(), err)
			}
			log.Printf("[DEBUG] added security group (%s) to instance (%s)", g, d.Id())
		}
	}

	if d.HasChange("admin_pass") {
		if newPwd, ok := d.Get("admin_pass").(string); ok {
			err := cloudservers.ChangeAdminPassword(ecsClient, d.Id(), newPwd).ExtractErr()
			if err != nil {
				return diag.Errorf("error changing admin password of server (%s): %s", d.Id(), err)
			}
		}
	}

	if d.HasChanges("flavor_id", "flavor_name") {
		newFlavorId, err := getFlavorID(d)
		if err != nil {
			return diag.FromErr(err)
		}

		extendParam := &cloudservers.ResizeExtendParam{
			AutoPay: common.GetAutoPay(d),
		}
		resizeOpts := &cloudservers.ResizeOpts{
			FlavorRef:   newFlavorId,
			Mode:        "withStopServer",
			ExtendParam: extendParam,
		}
		log.Printf("[DEBUG] resize configuration: %#v", resizeOpts)
		job, err := cloudservers.Resize(ecsV11Client, resizeOpts, d.Id()).ExtractJobResponse()
		if err != nil {
			return diag.Errorf("error resizing server: %s", err)
		}

		if err := cloudservers.WaitForJobSuccess(ecsClient, int(d.Timeout(schema.TimeoutUpdate)/time.Second), job.JobID); err != nil {
			return diag.Errorf("error waiting for instance (%s) to be resized: %s", d.Id(), err)
		}
	}

	if d.HasChange("network") {
		var err error
		nicClient, err := cfg.NetworkingV2Client(region)
		if err != nil {
			return diag.Errorf("error creating networking client: %s", err)
		}

		if err := updateSourceDestCheck(d, nicClient); err != nil {
			return diag.FromErr(err)
		}
	}

	if d.HasChange("tags") {
		ecsClient, err := cfg.ComputeV1Client(region)
		if err != nil {
			return diag.Errorf("error creating compute v1 client: %s", err)
		}

		tagErr := utils.UpdateResourceTags(ecsClient, d, "cloudservers", d.Id())
		if tagErr != nil {
			return diag.Errorf("error updating tags of instance:%s, err:%s", d.Id(), err)
		}
	}

	if d.HasChange("system_disk_size") {
		extendOpts := cloudvolumes.ExtendOpts{
			SizeOpts: cloudvolumes.ExtendSizeOpts{
				NewSize: d.Get("system_disk_size").(int),
			},
		}

		if strings.EqualFold(d.Get("charging_mode").(string), "prePaid") {
			extendOpts.ChargeInfo = &cloudvolumes.ExtendChargeOpts{
				IsAutoPay: common.GetAutoPay(d),
			}
		}

		evsV2Client, err := cfg.BlockStorageV2Client(region)
		if err != nil {
			return diag.Errorf("error creating evs V2 client: %s", err)
		}
		evsV21Client, err := cfg.BlockStorageV21Client(region)
		if err != nil {
			return diag.Errorf("error creating evs V2.1 client: %s", err)
		}

		systemDiskID := d.Get("system_disk_id").(string)

		resp, err := cloudvolumes.ExtendSize(evsV21Client, systemDiskID, extendOpts).Extract()
		if err != nil {
			return diag.Errorf("error extending EVS volume (%s) size: %s", systemDiskID, err)
		}

		if strings.EqualFold(d.Get("charging_mode").(string), "prePaid") {
			bssClient, err := cfg.BssV2Client(region)
			if err != nil {
				return diag.Errorf("error creating BSS v2 client: %s", err)
			}
			err = common.WaitOrderComplete(ctx, bssClient, resp.OrderID, d.Timeout(schema.TimeoutUpdate))
			if err != nil {
				return diag.Errorf("The order (%s) is not completed while extending system disk (%s) size: %#v",
					resp.OrderID, d.Id(), err)
			}
		}

		stateConf := &resource.StateChangeConf{
			Pending:    []string{"extending"},
			Target:     []string{"available", "in-use"},
			Refresh:    evs.CloudVolumeRefreshFunc(evsV2Client, systemDiskID),
			Timeout:    d.Timeout(schema.TimeoutUpdate),
			Delay:      10 * time.Second,
			MinTimeout: 3 * time.Second,
		}

		_, err = stateConf.WaitForStateContext(ctx)
		if err != nil {
			return diag.Errorf(
				"error waiting for huaweicloud_compute_instance system disk %s to become ready: %s", systemDiskID, err)
		}
	}

	// update the key_pair before power action
	if d.HasChange("key_pair") {
		kmsClient, err := cfg.KmsV3Client(region)
		if err != nil {
			return diag.Errorf("error creating KMS v3 client: %s", err)
		}

		o, n := d.GetChange("key_pair")
		keyPairOpts := &common.KeypairAuthOpts{
			InstanceID:       d.Id(),
			InUsedKeyPair:    o.(string),
			NewKeyPair:       n.(string),
			InUsedPrivateKey: d.Get("private_key").(string),
			Password:         d.Get("admin_pass").(string),
			Timeout:          d.Timeout(schema.TimeoutUpdate),
		}
		if err := common.UpdateEcsInstanceKeyPair(ctx, ecsClient, kmsClient, keyPairOpts); err != nil {
			return diag.FromErr(err)
		}
	}

	// The instance power status update needs to be done at the end
	if d.HasChange("power_action") {
		action := d.Get("power_action").(string)
		if err = doPowerAction(ecsClient, d, action); err != nil {
			return diag.Errorf("Doing power action (%s) for instance (%s) failed: %s", action, d.Id(), err)
		}
	}

	if d.HasChange("auto_renew") {
		bssClient, err := cfg.BssV2Client(region)
		if err != nil {
			return diag.Errorf("error creating BSS V2 client: %s", err)
		}
		if err = common.UpdateAutoRenew(bssClient, d.Get("auto_renew").(string), d.Id()); err != nil {
			return diag.Errorf("error updating the auto-renew of the instance (%s): %s", d.Id(), err)
		}
	}

	return resourceComputeInstanceRead(ctx, d, meta)
}

func resourceComputeInstanceDelete(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
	cfg := meta.(*config.Config)
	region := cfg.GetRegion(d)
	ecsClient, err := cfg.ComputeV1Client(region)
	if err != nil {
		return diag.Errorf("error creating compute client: %s", err)
	}

	if d.Get("stop_before_destroy").(bool) {
		if err = doPowerAction(ecsClient, d, "FORCE-OFF"); err != nil {
			log.Printf("[WARN] error stopping instance: %s", err)
		} else {
			log.Printf("[DEBUG] waiting for instance (%s) to stop", d.Id())
			pending := []string{"ACTIVE"}
			target := []string{"SHUTOFF"}
			timeout := d.Timeout(schema.TimeoutDelete)
			if err := waitForServerTargetState(ctx, ecsClient, d.Id(), pending, target, timeout); err != nil {
				return diag.Errorf("State waiting timeout: %s", err)
			}
		}
	}

	if d.Get("charging_mode") == "prePaid" {
		resources, err := calcUnsubscribeResources(d, cfg)
		if err != nil {
			return diag.Errorf("error unsubscribe ECS server: %s", err)
		}

		log.Printf("[DEBUG] %v will be unsubscribed", resources)
		if err := common.UnsubscribePrePaidResource(d, cfg, resources); err != nil {
			return diag.Errorf("error unsubscribe ECS server: %s", err)
		}
	} else {
		serverRequests := []cloudservers.Server{
			{Id: d.Id()},
		}

		deleteOpts := cloudservers.DeleteOpts{
			Servers:        serverRequests,
			DeleteVolume:   d.Get("delete_disks_on_termination").(bool),
			DeletePublicIP: d.Get("delete_eip_on_termination").(bool),
		}

		n, err := cloudservers.Delete(ecsClient, deleteOpts).ExtractJobResponse()
		if err != nil {
			return diag.Errorf("error deleting server: %s", err)
		}

		if err := cloudservers.WaitForJobSuccess(ecsClient, int(d.Timeout(schema.TimeoutCreate)/time.Second), n.JobID); err != nil {
			return diag.FromErr(err)
		}
	}

	// Instance may still exist after Order/Job succeed.
	pending := []string{"ACTIVE", "SHUTOFF"}
	target := []string{"DELETED", "SOFT_DELETED"}
	deleteTimeout := d.Timeout(schema.TimeoutDelete)
	if err := waitForServerTargetState(ctx, ecsClient, d.Id(), pending, target, deleteTimeout); err != nil {
		return diag.Errorf("State waiting timeout: %s", err)
	}

	d.SetId("")
	return nil
}

func resourceComputeInstanceImportState(_ context.Context, d *schema.ResourceData, meta interface{}) ([]*schema.ResourceData, error) {
	cfg := meta.(*config.Config)
	region := cfg.GetRegion(d)
	ecsClient, err := cfg.ComputeV1Client(region)
	if err != nil {
		return nil, fmt.Errorf("error creating compute client: %s", err)
	}

	server, err := cloudservers.Get(ecsClient, d.Id()).Extract()
	if err != nil {
		return nil, common.CheckDeleted(d, err, "compute instance")
	}

	allInstanceNics, err := getInstanceAddresses(d, meta, server)
	if err != nil {
		return nil, fmt.Errorf("error fetching networks of compute instance %s: %s", d.Id(), err)
	}

	networks := []map[string]interface{}{}
	for _, nic := range allInstanceNics {
		v := map[string]interface{}{
			"uuid":              nic.NetworkID,
			"port":              nic.PortID,
			"fixed_ip_v4":       nic.FixedIPv4,
			"fixed_ip_v6":       nic.FixedIPv6,
			"ipv6_enable":       nic.FixedIPv6 != "",
			"source_dest_check": nic.SourceDestCheck,
			"mac":               nic.MAC,
		}
		networks = append(networks, v)
	}

	log.Printf("[DEBUG] flatten Instance Networks: %#v", networks)
	d.Set("network", networks)

	return []*schema.ResourceData{d}, nil
}

// ServerV1StateRefreshFunc returns a resource.StateRefreshFunc that is used to watch an HuaweiCloud instance.
func ServerV1StateRefreshFunc(client *golangsdk.ServiceClient, instanceID string) resource.StateRefreshFunc {
	return func() (interface{}, string, error) {
		s, err := cloudservers.Get(client, instanceID).Extract()
		if err != nil {
			if _, ok := err.(golangsdk.ErrDefault404); ok {
				return s, "DELETED", nil
			}
			return nil, "", err
		}

		// get fault message when status is ERROR
		if s.Status == "ERROR" {
			fault := fmt.Errorf("error code: %d, message: %s", s.Fault.Code, s.Fault.Message)
			return s, "ERROR", fault
		}
		return s, s.Status, nil
	}
}

func resourceInstanceSecGroupIdsV1(client *golangsdk.ServiceClient, d *schema.ResourceData) ([]cloudservers.SecurityGroup, error) {
	if v, ok := d.GetOk("security_group_ids"); ok {
		rawSecGroups := v.(*schema.Set).List()
		secGroups := make([]cloudservers.SecurityGroup, len(rawSecGroups))
		for i, raw := range rawSecGroups {
			secGroups[i] = cloudservers.SecurityGroup{
				ID: raw.(string),
			}
		}
		return secGroups, nil
	}

	rawSecGroups := d.Get("security_groups").(*schema.Set).List()
	secGroups := make([]cloudservers.SecurityGroup, 0, len(rawSecGroups))

	opt := groups.ListOpts{
		EnterpriseProjectId: "all_granted_eps",
	}
	pages, err := groups.List(client, opt).AllPages()
	if err != nil {
		return nil, err
	}
	resp, err := groups.ExtractSecurityGroups(pages)
	if err != nil {
		return nil, err
	}

	for _, raw := range rawSecGroups {
		secName := raw.(string)
		for _, secGroup := range resp {
			if secName == secGroup.Name {
				secGroups = append(secGroups, cloudservers.SecurityGroup{
					ID: secGroup.ID,
				})
				break
			}
		}
	}
	if len(secGroups) != len(rawSecGroups) {
		return nil, fmt.Errorf("the list contains invalid security groups (num: %d), please check your entry",
			len(rawSecGroups)-len(secGroups))
	}

	return secGroups, nil
}

func getOpSvcUserID(d *schema.ResourceData, conf *config.Config) string {
	if v, ok := d.GetOk("user_id"); ok {
		return v.(string)
	}
	return conf.UserID
}

func validateComputeInstanceConfig(d *schema.ResourceData, conf *config.Config) error {
	_, hasSSH := d.GetOk("key_pair")
	if d.Get("charging_mode").(string) == "prePaid" && hasSSH {
		if getOpSvcUserID(d, conf) == "" {
			return fmt.Errorf("user_id must be specified when charging_mode is set to prePaid and " +
				"the ECS is logged in using an SSH key")
		}
	}

	return nil
}

func buildInstanceNicsRequest(d *schema.ResourceData) []cloudservers.Nic {
	var nicRequests []cloudservers.Nic

	networks := d.Get("network").([]interface{})
	for _, v := range networks {
		network := v.(map[string]interface{})
		nicRequest := cloudservers.Nic{
			SubnetId:   network["uuid"].(string),
			IpAddress:  network["fixed_ip_v4"].(string),
			Ipv6Enable: network["ipv6_enable"].(bool),
		}

		nicRequests = append(nicRequests, nicRequest)
	}
	return nicRequests
}

func buildInstancePublicIPRequest(d *schema.ResourceData) *cloudservers.PublicIp {
	if v, ok := d.GetOk("eip_id"); ok {
		return &cloudservers.PublicIp{
			Id: v.(string),
		}
	}

	bandWidthRaw := d.Get("bandwidth").([]interface{})
	if len(bandWidthRaw) != 1 {
		return nil
	}

	bandWidth := bandWidthRaw[0].(map[string]interface{})
	bwOpts := cloudservers.BandWidth{
		ShareType:  bandWidth["share_type"].(string),
		Id:         bandWidth["id"].(string),
		ChargeMode: bandWidth["charge_mode"].(string),
		Size:       bandWidth["size"].(int),
	}

	return &cloudservers.PublicIp{
		Eip: &cloudservers.Eip{
			IpType:    d.Get("eip_type").(string),
			BandWidth: &bwOpts,
		},
		DeleteOnTermination: d.Get("delete_eip_on_termination").(bool),
	}
}

func resourceInstanceSecGroupsV2(d *schema.ResourceData) []string {
	rawSecGroups := d.Get("security_groups").(*schema.Set).List()
	secGroups := make([]string, len(rawSecGroups))
	for i, raw := range rawSecGroups {
		secGroups[i] = raw.(string)
	}
	return secGroups
}

func resourceInstanceMetadataV2(d *schema.ResourceData) map[string]string {
	m := make(map[string]string)
	for key, val := range d.Get("metadata").(map[string]interface{}) {
		m[key] = val.(string)
	}
	return m
}

func resourceInstanceBlockDevicesV2(bds []interface{}) ([]bootfromvolume.BlockDevice, error) {
	blockDeviceOpts := make([]bootfromvolume.BlockDevice, len(bds))
	for i, bd := range bds {
		bdM := bd.(map[string]interface{})
		blockDeviceOpts[i] = bootfromvolume.BlockDevice{
			UUID:                bdM["uuid"].(string),
			VolumeSize:          bdM["volume_size"].(int),
			BootIndex:           bdM["boot_index"].(int),
			DeleteOnTermination: bdM["delete_on_termination"].(bool),
		}

		sourceType := bdM["source_type"].(string)
		switch sourceType {
		case "blank":
			blockDeviceOpts[i].SourceType = bootfromvolume.SourceBlank
		case "image":
			blockDeviceOpts[i].SourceType = bootfromvolume.SourceImage
		case "snapshot":
			blockDeviceOpts[i].SourceType = bootfromvolume.SourceSnapshot
		case "volume":
			blockDeviceOpts[i].SourceType = bootfromvolume.SourceVolume
		default:
			return blockDeviceOpts, fmt.Errorf("unknown block device source type %s", sourceType)
		}

		destinationType := bdM["destination_type"].(string)
		switch destinationType {
		case "local":
			blockDeviceOpts[i].DestinationType = bootfromvolume.DestinationLocal
		case "volume":
			blockDeviceOpts[i].DestinationType = bootfromvolume.DestinationVolume
		default:
			return blockDeviceOpts, fmt.Errorf("unknown block device destination type %s", destinationType)
		}
	}

	log.Printf("[DEBUG] block device options: %+v", blockDeviceOpts)
	return blockDeviceOpts, nil
}

func resourceInstanceSchedulerHintsV1(schedulerHintsRaw map[string]interface{}) cloudservers.SchedulerHints {
	schedulerHints := cloudservers.SchedulerHints{
		Group:           schedulerHintsRaw["group"].(string),
		FaultDomain:     schedulerHintsRaw["fault_domain"].(string),
		Tenancy:         schedulerHintsRaw["tenancy"].(string),
		DedicatedHostID: schedulerHintsRaw["deh_id"].(string),
	}

	return schedulerHints
}

func resourceInstanceSchedulerHintsV2(schedulerHintsRaw map[string]interface{}) schedulerhints.SchedulerHints {
	schedulerHints := schedulerhints.SchedulerHints{
		Group:           schedulerHintsRaw["group"].(string),
		Tenancy:         schedulerHintsRaw["tenancy"].(string),
		DedicatedHostID: schedulerHintsRaw["deh_id"].(string),
	}

	return schedulerHints
}

func getImage(client *golangsdk.ServiceClient, id, name string) (*cloudimages.Image, error) {
	listOpts := &cloudimages.ListOpts{
		ID:    id,
		Name:  name,
		Limit: 1,
	}
	allPages, err := cloudimages.List(client, listOpts).AllPages()
	if err != nil {
		return nil, fmt.Errorf("unable to query images: %s", err)
	}

	allImages, err := cloudimages.ExtractImages(allPages)
	if err != nil {
		return nil, fmt.Errorf("unable to retrieve images: %s", err)
	}

	if len(allImages) < 1 {
		return nil, fmt.Errorf("unable to find images %s: Maybe not existed", id)
	}

	img := allImages[0]
	if id != "" && img.ID != id {
		return nil, fmt.Errorf("unexpected images ID")
	}
	if name != "" && img.Name != name {
		return nil, fmt.Errorf("unexpected images Name")
	}
	log.Printf("[DEBUG] retrieved image %s: %#v", id, img)
	return &img, nil
}

func getImageIDFromConfig(imsClient *golangsdk.ServiceClient, d *schema.ResourceData) (string, error) {
	// If block_device_mapping_v2 was used, an Image does not need to be specified, unless an image/local
	// combination was used. This emulates normal boot behavior. Otherwise, ignore the image altogether.
	if vL, ok := d.GetOk("block_device_mapping_v2"); ok {
		needImage := false
		for _, v := range vL.([]interface{}) {
			vM := v.(map[string]interface{})
			if vM["source_type"] == "image" && vM["destination_type"] == "local" {
				needImage = true
			}
		}
		if !needImage {
			return "", nil
		}
	}

	if imageID := d.Get("image_id").(string); imageID != "" {
		return imageID, nil
	}

	if imageName := d.Get("image_name").(string); imageName != "" {
		img, err := getImage(imsClient, "", imageName)
		if err != nil {
			return "", err
		}
		return img.ID, nil
	}

	return "", fmt.Errorf("neither a boot device, image ID, or image name were able to be determined")
}

func setImageInformation(d *schema.ResourceData, imsClient *golangsdk.ServiceClient, imageID string) error {
	// If block_device_mapping_v2 was used, an Image does not need to be specified, unless an image/local
	// combination was used. This emulates normal boot behavior. Otherwise, ignore the image altogether.
	if vL, ok := d.GetOk("block_device_mapping_v2"); ok {
		needImage := false
		for _, v := range vL.([]interface{}) {
			vM := v.(map[string]interface{})
			if vM["source_type"] == "image" && vM["destination_type"] == "local" {
				needImage = true
			}
		}
		if !needImage {
			d.Set("image_id", "Attempt to boot from volume - no image supplied")
			return nil
		}
	}

	if imageID != "" {
		d.Set("image_id", imageID)
		image, err := getImage(imsClient, imageID, "")
		if err != nil {
			// If the image name can't be found, set the value to "Image not found".
			// The most likely scenario is that the image no longer exists in the Image Service
			// but the instance still has a record from when it existed.
			d.Set("image_name", "Image not found")
			return nil
		}
		d.Set("image_name", image.Name)
	}

	return nil
}

// computePublicIP get the first floating address
func computePublicIP(server *cloudservers.CloudServer) string {
	var publicIP string

	for _, addresses := range server.Addresses {
		for _, addr := range addresses {
			if addr.Type == "floating" {
				publicIP = addr.Addr
				break
			}
		}
	}

	return publicIP
}

func getFlavorID(d *schema.ResourceData) (string, error) {
	var flavorID string

	// both flavor_id and flavor_name are the same value
	if v1, ok := d.GetOk("flavor_id"); ok {
		flavorID = v1.(string)
	} else if v2, ok := d.GetOk("flavor_name"); ok {
		flavorID = v2.(string)
	}

	if flavorID == "" {
		return "", fmt.Errorf("missing required argument: the `flavor_id` must be specified")
	}
	return flavorID, nil
}

func getVpcID(client *golangsdk.ServiceClient, d *schema.ResourceData) (string, error) {
	var networkID string

	networks := d.Get("network").([]interface{})
	if len(networks) > 0 {
		// all networks belongs to one VPC
		network := networks[0].(map[string]interface{})
		networkID = network["uuid"].(string)
	}

	if networkID == "" {
		return "", fmt.Errorf("network ID should not be empty")
	}

	subnet, err := subnets.Get(client, networkID).Extract()
	if err != nil {
		return "", fmt.Errorf("error retrieving subnets: %s", err)
	}

	return subnet.VPC_ID, nil
}

func resourceComputeSchedulerHintsHash(v interface{}) int {
	var buf bytes.Buffer
	m := v.(map[string]interface{})

	if m["group"] != nil {
		buf.WriteString(fmt.Sprintf("%s-", m["group"].(string)))
	}

	if m["tenancy"] != nil {
		buf.WriteString(fmt.Sprintf("%s-", m["tenancy"].(string)))
	}

	if m["deh_id"] != nil {
		buf.WriteString(fmt.Sprintf("%s-", m["deh_id"].(string)))
	}

	return hashcode.String(buf.String())
}

func checkBlockDeviceConfig(d *schema.ResourceData) error {
	if vL, ok := d.GetOk("block_device_mapping_v2"); ok {
		for _, v := range vL.([]interface{}) {
			vM := v.(map[string]interface{})

			if vM["source_type"] != "blank" && vM["uuid"] == "" {
				return fmt.Errorf("you must specify a uuid for %s block device types", vM["source_type"])
			}

			if vM["source_type"] == "image" && vM["destination_type"] == "volume" {
				if vM["volume_size"] == 0 {
					return fmt.Errorf("you must specify a volume_size when creating a volume from an image")
				}
			}

			if vM["source_type"] == "blank" && vM["destination_type"] == "local" {
				if vM["volume_size"] == 0 {
					return fmt.Errorf("you must specify a volume_size when creating a blank block device")
				}
			}
		}
	}

	return nil
}

func waitForServerTargetState(ctx context.Context, client *golangsdk.ServiceClient, instanceID string, pending, target []string,
	timeout time.Duration) error {
	stateConf := &resource.StateChangeConf{
		Pending:      pending,
		Target:       target,
		Refresh:      ServerV1StateRefreshFunc(client, instanceID),
		Timeout:      timeout,
		Delay:        5 * time.Second,
		PollInterval: 5 * time.Second,
	}

	_, err := stateConf.WaitForStateContext(ctx)
	if err != nil {
		return fmt.Errorf("error waiting for instance (%s) to become target state (%v): %s", instanceID, target, err)
	}
	return nil
}

// doPowerAction is a method for instance power doing shutdown, startup and reboot actions.
func doPowerAction(client *golangsdk.ServiceClient, d *schema.ResourceData, action string) error {
	var jobResp *cloudservers.JobResponse
	powerOpts := powers.PowerOpts{
		Servers: []powers.ServerInfo{
			{ID: d.Id()},
		},
	}
	// In the reboot structure, Type is a required option.
	// Since the type of power off and reboot is 'SOFT' by default, setting this value has solved the power structural
	// compatibility problem between optional and required.
	if action != "ON" {
		powerOpts.Type = "SOFT"
	}
	if strings.HasPrefix(action, "FORCE-") {
		powerOpts.Type = "HARD"
		action = strings.TrimPrefix(action, "FORCE-")
	}
	op, ok := powerActionMap[action]
	if !ok {
		return fmt.Errorf("the powerMap does not contain option (%s)", action)
	}
	jobResp, err := powers.PowerAction(client, powerOpts, op).ExtractJobResponse()
	if err != nil {
		return fmt.Errorf("doing power action (%s) for instance (%s) failed: %s", action, d.Id(), err)
	}

	// The time of the power on/off and reboot is usually between 15 and 35 seconds.
	timeout := 3 * time.Minute
	if err := cloudservers.WaitForJobSuccess(client, int(timeout/time.Second), jobResp.JobID); err != nil {
		return fmt.Errorf("waiting power action (%s) for instance (%s) failed: %s", action, d.Id(), err)
	}
	return nil
}

func disableSourceDestCheck(networkClient *golangsdk.ServiceClient, portID string) error {
	// Update the allowed-address-pairs of the port to 1.1.1.1/0
	// to disable the source/destination check
	portpairs := []ports.AddressPair{
		{
			IPAddress: "1.1.1.1/0",
		},
	}
	portUpdateOpts := ports.UpdateOpts{
		AllowedAddressPairs: &portpairs,
	}

	_, err := ports.Update(networkClient, portID, portUpdateOpts).Extract()
	return err
}

func enableSourceDestCheck(networkClient *golangsdk.ServiceClient, portID string) error {
	// cancle all allowed-address-pairs to enable the source/destination check
	portpairs := make([]ports.AddressPair, 0)
	portUpdateOpts := ports.UpdateOpts{
		AllowedAddressPairs: &portpairs,
	}

	_, err := ports.Update(networkClient, portID, portUpdateOpts).Extract()
	return err
}

func updateSourceDestCheck(d *schema.ResourceData, client *golangsdk.ServiceClient) error {
	var err error

	networks := d.Get("network").([]interface{})
	for i, v := range networks {
		nic := v.(map[string]interface{})
		nicPort := nic["port"].(string)
		if nicPort == "" {
			continue
		}

		if d.HasChange(fmt.Sprintf("network.%d.source_dest_check", i)) {
			sourceDestCheck := nic["source_dest_check"].(bool)
			if !sourceDestCheck {
				err = disableSourceDestCheck(client, nicPort)
			} else {
				err = enableSourceDestCheck(client, nicPort)
			}

			if err != nil {
				return fmt.Errorf("error updating source_dest_check on port(%s) of instance(%s) failed: %s", nicPort, d.Id(), err)
			}
		}
	}

	return nil
}

func calcUnsubscribeResources(d *schema.ResourceData, cfg *config.Config) ([]string, error) {
	var mainResources = []string{d.Id()}

	if shouldUnsubscribeEIP(d) {
		region := cfg.GetRegion(d)
		eipClient, err := cfg.NetworkingV1Client(region)
		if err != nil {
			return nil, fmt.Errorf("error creating networking client: %s", err)
		}

		eipAddr := d.Get("public_ip").(string)
		eipID, err := common.GetEipIDbyAddress(eipClient, eipAddr, "all_granted_eps")
		if err != nil {
			return nil, fmt.Errorf("error fetching EIP ID of ECS (%s): %s", d.Id(), err)
		}

		mainResources = append(mainResources, eipID)
	}

	return mainResources, nil
}

func shouldUnsubscribeEIP(d *schema.ResourceData) bool {
	deleteEIP := d.Get("delete_eip_on_termination").(bool)
	eipAddr := d.Get("public_ip").(string)
	eipType := d.Get("eip_type").(string)
	_, sharebw := d.GetOk("bandwidth.0.id")

	return deleteEIP && eipAddr != "" && eipType != "" && !sharebw
}

func flattenTagsToMap(tags []string) map[string]string {
	result := make(map[string]string)

	for _, tagStr := range tags {
		tag := strings.SplitN(tagStr, "=", 2)
		if len(tag) == 1 {
			result[tag[0]] = ""
		} else if len(tag) == 2 {
			result[tag[0]] = tag[1]
		}
	}

	return result
}

func resourceInstanceRootVolume(d *schema.ResourceData) cloudservers.RootVolume {
	diskType := d.Get("system_disk_type").(string)
	if diskType == "" {
		diskType = "GPSSD"
	}
	volRequest := cloudservers.RootVolume{
		VolumeType: diskType,
		Size:       d.Get("system_disk_size").(int),
	}
	return volRequest
}

func resourceInstanceDataVolumes(d *schema.ResourceData) []cloudservers.DataVolume {
	var volRequests []cloudservers.DataVolume

	vols := d.Get("data_disks").([]interface{})
	for i := range vols {
		vol := vols[i].(map[string]interface{})
		volRequest := cloudservers.DataVolume{
			VolumeType: vol["type"].(string),
			Size:       vol["size"].(int),
		}
		if vol["snapshot_id"] != "" {
			extendparam := cloudservers.VolumeExtendParam{
				SnapshotId: vol["snapshot_id"].(string),
			}
			volRequest.Extendparam = &extendparam
		}

		if vol["kms_key_id"] != "" {
			matadata := cloudservers.VolumeMetadata{
				SystemEncrypted: "1",
				SystemCmkid:     vol["kms_key_id"].(string),
			}
			volRequest.Metadata = &matadata
		}

		volRequests = append(volRequests, volRequest)
	}
	return volRequests
}
