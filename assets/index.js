$(document).ready(function(){
  var poll = function() {
    console.log("polling...");
    var runId = $("p#run-id").text();
    if (runId == "") {
      console.log("no run id");
      return
    }
    console.log(runId)
    var url = "/runs/" + runId;
    $.ajax({
        url: url,
        type: "GET",
        success: function(data) {
          var notNull = (data.status != undefined && data.status != null && data.status != "")
          if (notNull) {
            console.log(data.status)
            $("p#status").text(data.status);
            var outputs = "<ul>";
            for (let i = 0; i < data.outputs.length; i++) {
              outputs += "<li>" + data.outputs[i] + "</li>";
            }
            outputs += "</ul>";
            $("p#output").html(outputs);
          }
        },
        dataType: "json",
        timeout: 2000
    })
  }
  setInterval(poll, 5000);

  $("button#share").click(function() {
    var config = $("#configuration").val();
    console.log(config);
    $.ajax({
        url: "/share",
        type: "POST",
        data: JSON.stringify({
          config: config,
        }),
        success: function(data) {
          console.log(data)
          if (data == undefined || data == null) {
            $("p#shareurl").text("Could not get sharing information");
          }
          if (data.hasconfig == false) {
            $("p#shareurl").text("You do not have any configuration information");
          } else {
            var url = window.location.origin + "?share_id="+data.id;
            $("p#shareurl").text(url);
          }
        },
        dataType: "json",
        timeout: 2000
    })
  });
});
