package main

import (
	"log"
	"net/http"
	"os"
	"reflect"
	"strings"
	"time"

	common "foremast.ai/foremast/foremast-service/pkg/common"
	converter "foremast.ai/foremast/foremast-service/pkg/converter"
	models "foremast.ai/foremast/foremast-service/pkg/models"
	prometheus "foremast.ai/foremast/foremast-service/pkg/prometheus"
	search "foremast.ai/foremast/foremast-service/pkg/search"
	"github.com/gin-gonic/gin"
	"github.com/olivere/elastic"
)

var (
	elasticClient *elastic.Client
)

// ConfigSeparator .... constant variable based on to separate the queries
const ConfigSeparator  = " ||"
// KvSeparator   .... used for key and value separate
const KvSeparator = "== "

func constructURL(metricQuery models.MetricQuery) (int32, string) {
	config := metricQuery.Parameters
	if config == nil || len(config) == 0 {
		return 404, ""
	}

	if metricQuery.DataSourceType == "prometheus" {
		return 0, prometheus.BuildURL(metricQuery)
	}
	//type is not supported
	return 404, ""
}

func convertMetricQuerys(metric map[string]models.MetricQuery) (int32, string) {
	if len(metric) == 0 {
		return 404, ""
	}
	output := strings.Builder{}
	var co int
	for key, value := range metric {
		errCode, retstr := constructURL(value)
		if errCode != 0 {
			return 404, ""
		}
		if co == 1 {
			output.WriteString(ConfigSeparator )
		}
		output.WriteString(key)
		output.WriteString(KvSeparator )
		output.WriteString(retstr)
		co = 1
	}
	return 0, output.String()
}

func convertMetricInfoString(m models.MetricsInfo, strategy string) (int, string, []string) {

	configs := []string{"", "", ""}
	if m.Current == nil || len(m.Current) == 0 {
		return 404, "MetricInfo current is empty ", configs
	}
	errorCode := 0
	reason := strings.Builder{}
	errCode, ret := convertMetricQuerys(m.Current)

	if errCode != 0 {
		log.Println("Error: current convertMetricQuerys ", m.Current, " failed. errorCode is ", errCode)
		reason.WriteString("current query encount error ")
		reason.WriteString(ret)
		reason.WriteString("\n")
		errorCode = 404
	}
	configs[0] = ret

	if m.Baseline != nil {
		errCode, ret := convertMetricQuerys(m.Baseline)
		if errCode != 0 {
			log.Println("Warning: baseline convertMetricQuerys ", m.Baseline, " failed. errorCode is ", errCode)
			reason.WriteString(" baseline query encount error ")
			reason.WriteString(ret)
		}
		configs[1] = ret
	}

	if m.Historical != nil {
		hErrCode, ret := convertMetricQuerys(m.Historical)
		if hErrCode != 0 {
			log.Println("Warning: historical convertMetricQuerys ", m.Historical, " failed. errorCode is ", hErrCode)
			reason.WriteString(" historical query encount error ")
			reason.WriteString(ret)
		}
		if errCode != 0 && hErrCode != 0 {
			errorCode = 404
		}
		configs[2] = ret
	} else {
		if errCode != 0 {
			errorCode = 404
		}
	}

	return errorCode, reason.String(), configs
}

// RegisterEntry .... mapping input request to elasticserch structure
func RegisterEntry(context *gin.Context) {
	var appRequest models.ApplicationHealthAnalyzeRequest
	//check bad request
	if err := context.BindJSON(&appRequest); err != nil {
		log.Println("Error: encounter context error ", err, " detail ", reflect.TypeOf(err))
		common.ErrorResponse(context, http.StatusBadRequest, "Bad request")
		return
	}
	//check appName
	if common.CheckStrEmpty(appRequest.AppName) {
		log.Println("Error: appName is empty")
		common.ErrorResponse(context, http.StatusBadRequest, "appName is empty")
		return
	}
	//check metric query
	errCode, reason, configs := convertMetricInfoString(appRequest.Metrics, appRequest.Strategy)
	if errCode != 0 {
		log.Println("encount error while convertMetricInfoString ", reason)
		common.ErrorResponse(context, http.StatusBadRequest, reason)
		return
	}

	doc := models.DocumentRequest{
		appRequest.AppName,
		appRequest.StartTime,
		appRequest.EndTime,
		configs[0],
		configs[1],
		configs[2],
		"200",
		appRequest.Strategy,
	}
	id, err := search.CreateNewDoc(context, elasticClient, doc)
	context.JSON(http.StatusOK, converter.ConvertESToNewResp(id, err, "new", ""))

}

// SearchByID .... restful serach by uuid or job id
func SearchByID(context *gin.Context) {
	_id := context.Param("id")
	log.Println("Search by id got called :" + _id + "\n")
	doc, err, reason := search.ByID(context, elasticClient, _id)

	if err != 0 {
		if err == -1 {
			context.JSON(http.StatusOK, converter.ConvertESToNewResp(_id, 200, "unknown", _id+" not found."))
		} else {
			context.JSON(http.StatusOK, converter.ConvertESToNewResp(_id, 404, "unknown", reason))
		}
		return
	}
	context.JSON(http.StatusOK, converter.ConvertESToResp(doc))

}

// main .... program entry
func main() {
	var esURL = os.Getenv("ELASTIC_URL")
	if esURL == "" {
		esURL = "http://a31008275fcf911e8bde30674acac93e-885155939.us-west-2.elb.amazonaws.com:9200"
	}

	var err error
	// Create Elastic client and wait for Elasticsearch to be ready
	for {
		elasticClient, err = elastic.NewClient(
			elastic.SetURL(esURL),
			elastic.SetSniff(false),
		)
		if err != nil {
			log.Println("failed to reach elasticsearch endpoint ", err)
			// Retry every 3 seconds
			time.Sleep(3 * time.Second)
		} else {
			break
		}
	}
	router := gin.Default()
	v1 := router.Group("/v1/healthcheck")
	{
		//search by id
		v1.GET("/id/:id", SearchByID)
		//create request
		v1.POST("/create", RegisterEntry)
	}
	if err = router.Run(":8099"); err != nil {
		log.Fatal(err)
	}

}
