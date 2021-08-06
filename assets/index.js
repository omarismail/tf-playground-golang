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
});
