package sfsturbo

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/resource"

	"github.com/huaweicloud/terraform-provider-hcs/huaweicloudstack/services/acceptance"
	"github.com/huaweicloud/terraform-provider-hcs/huaweicloudstack/services/acceptance/common"
)

func TestAccTurbosDataSource_basic(t *testing.T) {
	var (
		rName         = acceptance.RandomAccResourceNameWithDash()
		dcByName      = acceptance.InitDataSourceCheck("data.hcs_sfs_turbos.by_name")
		dcBySize      = acceptance.InitDataSourceCheck("data.hcs_sfs_turbos.by_size")
		dcByShareType = acceptance.InitDataSourceCheck("data.hcs_sfs_turbos.by_share_type")
		dcByEpsId     = acceptance.InitDataSourceCheck("data.hcs_sfs_turbos.by_eps_id")
	)

	resource.ParallelTest(t, resource.TestCase{
		PreCheck: func() {
			acceptance.TestAccPreCheck(t)
			acceptance.TestAccPreCheckEpsID(t)
		},
		ProviderFactories: acceptance.TestAccProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccTurbosDataSource_basic(rName),
				Check: resource.ComposeTestCheckFunc(
					dcByName.CheckResourceExists(),
					resource.TestCheckOutput("name_query_result_validation", "true"),
					dcBySize.CheckResourceExists(),
					resource.TestCheckOutput("size_query_result_validation", "true"),
					dcByShareType.CheckResourceExists(),
					resource.TestCheckOutput("share_type_query_result_validation", "true"),
					dcByEpsId.CheckResourceExists(),
					resource.TestCheckOutput("eps_id_query_result_validation", "true"),
				),
			},
		},
	})
}

func testAccTurbosDataSource_basic(rName string) string {
	return fmt.Sprintf(`
%[1]s

variable "turbo_configuration" {
  type = list(object({
    size        = number
    share_type  = string
    eps_enabled = bool
  }))

  default = [
    {size = 100, share_type = "sfsturbo.hdd", bandwidth = 150, eps_enabled = false},
    {size = 200, share_type = "sfsturbo.ssd", bandwidth = 350, eps_enabled = false},
    {size = 200, share_type = "sfsturbo.ssd", bandwidth = 350, eps_enabled = true},
  ]
}

data "hcs_availability_zones" "test" {}

resource "hcs_sfs_turbo" "test" {
  count = length(var.turbo_configuration)

  vpc_id            = hcs_vpc.test.id
  subnet_id         = hcs_vpc_subnet.test.id
  security_group_id = hcs_networking_secgroup.test.id
  availability_zone = data.hcs_availability_zones.test.names[0]

  name                  = "%[2]s-${count.index}"
  size                  = var.turbo_configuration[count.index]["size"]
  share_proto           = "NFS"
  share_type            = var.turbo_configuration[count.index]["share_type"]
  enterprise_project_id = var.turbo_configuration[count.index]["eps_enabled"] ? "%[3]s" : "0"
}

data "hcs_sfs_turbos" "by_name" {
  depends_on = [hcs_sfs_turbo.test]

  name = hcs_sfs_turbo.test[0].name
}

data "hcs_sfs_turbos" "by_size" {
  depends_on = [hcs_sfs_turbo.test]

  size = var.turbo_configuration[0]["size"]
}

data "hcs_sfs_turbos" "by_share_type" {
  depends_on = [hcs_sfs_turbo.test]

  share_type = var.turbo_configuration[1]["share_type"]
}

data "hcs_sfs_turbos" "by_eps_id" {
  depends_on = [hcs_sfs_turbo.test]

  enterprise_project_id = "%[3]s"
}

output "name_query_result_validation" {
  value = contains(data.hcs_sfs_turbos.by_name.turbos[*].id,
  hcs_sfs_turbo.test[0].id) && !contains(data.hcs_sfs_turbos.by_name.turbos[*].id,
  hcs_sfs_turbo.test[1].id) && !contains(data.hcs_sfs_turbos.by_name.turbos[*].id,
  hcs_sfs_turbo.test[2].id)
}

output "size_query_result_validation" {
  value = contains(data.hcs_sfs_turbos.by_size.turbos[*].id,
  hcs_sfs_turbo.test[0].id) && !contains(data.hcs_sfs_turbos.by_size.turbos[*].id,
  hcs_sfs_turbo.test[1].id) && !contains(data.hcs_sfs_turbos.by_size.turbos[*].id,
  hcs_sfs_turbo.test[2].id)
}

output "share_type_query_result_validation" {
  value = contains(data.hcs_sfs_turbos.by_share_type.turbos[*].id,
  hcs_sfs_turbo.test[1].id) && !contains(data.hcs_sfs_turbos.by_share_type.turbos[*].id,
  hcs_sfs_turbo.test[0].id) && !contains(data.hcs_sfs_turbos.by_share_type.turbos[*].id,
  hcs_sfs_turbo.test[2].id)
}

output "eps_id_query_result_validation" {
  value = contains(data.hcs_sfs_turbos.by_eps_id.turbos[*].id,
  hcs_sfs_turbo.test[2].id)
}
`, common.TestBaseNetwork(rName), rName, acceptance.HCS_ENTERPRISE_PROJECT_ID_TEST)
}
