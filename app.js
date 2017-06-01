$(function() {
    var demo = false;
    var vis = (function(){
        var stateKey, eventKey, keys = {
            hidden: "visibilitychange",
            webkitHidden: "webkitvisibilitychange",
            mozHidden: "mozvisibilitychange",
            msHidden: "msvisibilitychange"
        };
        for (stateKey in keys) {
            if (stateKey in document) {
                eventKey = keys[stateKey];
                break;
            }
        }
        return function(c) {
            if (c) document.addEventListener(eventKey, c);
            return !document[stateKey];
        }
    })();

    Vue.component('chart', {
        template: "<div class=\"chart\"><span></span><div></div></div>",
        props: {
            peer: {
                type: Object,
                required: true,
            },
            live: {
                type: Boolean,
                default: false,
            },
            start: {
                type: Number,
                default: -6e+5,
            },
            maxDataPoints: {
                type: Number,
                default: 240,
            },
            size: {
                type: Number,
                default: 960,
            }
        },
        mounted: function() {
            this.isLive = false;
            this.webSocketState = 0;
            this.context = null;
            this.data = [];
            this.refreshInterval = this.peer.Interval < 1000 ? 1000 : this.peer.Interval;
            this.activeValueEl = this.$el.querySelector('span');
            this.redrawGraph = false;
            vis(this.visibilityChanged);
            this.getData(this.start, this.getTime());
        },
        watch: {
            start: function(val) {
                this.getData(val, this.getTime());
            },
        },
        methods: {
            getTime: function() {
                return new Date().getTime()
            },
            getData: function(start, stop) {
                if (start < 0) {
                    start = stop + start;
                }
                this.data = [];
                if (demo === true) {
                    this.demoMode(start, stop, this.maxDataPoints);
                } else {
                    this.getContinuesData(start, stop, this.maxDataPoints);
                }
            },
            getContinuesData: function(start, stop, max) {
                var $this = this;
                // reverse so, if we need to limit data it will be limited from the start
                $.getJSON("/data?peer=" + this.peer.ID + "&start=" + start + "&stop=" + stop + "&max=" + max, function(data) {
                    if (data != null && data.length > 0) {
                        if ($this.data.length == 0) {
                            $this.data = data;

                            var step = (stop-start) / max;
                            var lastTime = $this.data[0].Time;
                            while ($this.data.length < max) {
                                lastTime = lastTime - step;
                                $this.data.unshift({
                                    Time: lastTime,
                                    ResponseTime: 0,
                                });
                            }
                            $this.drawGraph();
                            $this.showSelectedValue($this.data.length - 1)
                        } else if ($this.insertData(data) === true) {
                            $this.drawGraph();
                            $this.showSelectedValue($this.data.length - 1)
                        }
                    }
                    
                    
                   if ($this.live === true) {
                        // if not connected
                        if ($this.webSocketState === 0) {
                            $this.connectToWebSocket();
                        }
                        // if not conencted or not connecting
                        if ($this.webSocketState <= 1) {
                            setTimeout(function() {
                                $this.getContinuesData(stop, $this.getTime(), max)    
                            }, $this.refreshInterval);
                        }
                    }

                });
            },
            connectToWebSocket: function() {
                var $this = this;
                console.log("Connecting to ws");
                this.webSocketState = 1;
                var ws = new WebSocket("ws://"+document.location.host+"/livedata");
                ws.onopen = function(e) {
                    var arr = new Uint8Array([
                        ($this.peer.ID & 0xff000000) >> 24,
                        ($this.peer.ID & 0x00ff0000) >> 16,
                        ($this.peer.ID & 0x0000ff00) >> 8,
                        ($this.peer.ID & 0x000000ff)
                    ]);
                    ws.send(arr.buffer);
                }
                ws.onmessage = function(e) {
                    $this.webSocketState = 2;
                    if ($this.insertSinglePoint(JSON.parse(e.data)) === true) {
                        $this.drawGraph();
                        $this.showSelectedValue($this.data.length - 1)
                    }
                };
                ws.onerror = function(e) {
                    $this.connectToWebSocket();
                }
                ws.onclose = function(e) {
                    $this.webSocketState = 0;
                    console.log("ws closed: ", e);
                    $this.connectToWebSocket();
                }
            },

            drawGraph: function() {
                if (vis()) {
                    var start = new Date(this.data[0].Time);
                    var end = new Date(this.data[this.data.length - 1].Time);
                    
                    var secondary_x_format = null;

                    
                    // more than a day
                    if ((start.getFullYear() != end.getFullYear()) || (start.getMonth() != end.getMonth())|| (start.getDay() != end.getDay())) {
                        secondary_x_format = d3.timeFormat('%d.%m.%Y');
                    } 

                    MG.data_graphic({
                        data: this.data,
                        target: this.$el.querySelector('div'),
                        chart_type: 'histogram',
                        width: this.size,
                        height: 200,
                        binned: true,
                        x_accessor: "Time",
                        y_accessor: "ResponseTime",
                        xax_format: d3.timeFormat('%H:%M:%S'),
                        secondary_x_format: secondary_x_format,
                        show_secondary_x_label: true,
                        animate_on_load: true,
                        transition_on_update: false,
                        mouseover: this.mouseover,
                        mouseout: this.mouseout,
                        show_rollover_text: false,
                        show_tooltips: false,
                        bottom: 36,
                        right: 30,
                        top: 4,
                    })
                    this.redrawGraph = false;
                } else {
                    this.redrawGraph = true;
                }
            },
            visibilityChanged: function() {
                if (vis() && this.redrawGraph == true) {
                    this.drawGraph()
                }
            },

            showSelectedValue: function(i) {
                this.activeValueEl.innerHTML = d3.timeFormat('%a %b %Y %H:%M:%S')(this.data[i].Time) + "\n" +  this.data[i].ResponseTime.toFixed(1) + "ms";
            },

            mouseover: function(d, i) {
                this.mouseOver = true;
                this.showSelectedValue(i)
            },

            mouseout: function() {
                this.mouseOver = false;
                this.showSelectedValue(this.data.length - 1)
            },

            insertSinglePoint: function(point) {
                // Is the new point too much in the past that we do not want it
                if (this.data[0].Time > point.Time) {
                    return false;
                }
                // Is the new point newer than the last point we got?
                // why not in loop you might ask: well we could avoid a splice call
                var len = this.data.length - 1;
                if (this.data[len].Time < point.Time) {
                    this.data.push(point)
                    this.data.unshift()
                    return true;
                }
                for (var i = len - 1; i >= 0; --i) {
                    if (this.data[i].Time < point.Time) {
                        this.data[i] = point
                        return true;
                    }
                }
                // something went wrong
                console.error("insertSinglePoint went wrong. Data: ", this.data, "Point: ", point)
                return false;
            },
            insertData: function(data) {                
                var ret = 0;
                for (var i = data.length - 1; i >= 0; --i) {
                   ret |= this.insertSinglePoint(data[i])
                }
                return ret !== 0;
            },
            demoMode: function(start, stop, max) {
                var step = (stop-start) / max;
                var lastTime = stop;
                while (this.data.length < max) {
                    lastTime = lastTime - step;
                    this.data.unshift({
                        Time: lastTime,
                        ResponseTime: Math.random() * 100 + 1,
                    });
                }

                this.drawGraph();
                this.showSelectedValue(this.data.length - 1)
                if (this.live === true) {
                    setInterval((function($this){
                        return function() {
                            var data = [{
                                Time: new Date().getTime(),
                                ResponseTime: Math.random() * 100 + 1,
                            }];
                            if ($this.insertData(data) === true) {
                                $this.drawGraph();
                                $this.showSelectedValue($this.data.length - 1)
                            }
                        };
                    })(this), 1000);
                }
            }

        }
    });


    var vm = new Vue({
        el: '#app',
        data: {
            peers: [],
        },
        created: function() {
            if (demo === false) {
                var $this = this;
                $.getJSON("/peers", function(peers) {
                    for (var i = peers.length - 1; i >= 0; i--) {
                        peers[i].history = -8.64e+7;
                        peers[i].AverageResponseTime = undefined;
                        peers[i].Uptime = undefined;
                        $.getJSON("/stats?peer=" + peers[i].ID, (function(id){
                            return function(stats) {
                                for (var i = $this.peers.length - 1; i >= 0; i--) {
                                    if ($this.peers[i].ID === id) {
                                        $this.peers[i].AverageResponseTime = stats.AverageResponseTime.toFixed(1);
                                        $this.peers[i].Uptime = stats.Uptime.toFixed(1);
                                        break;
                                    }
                                }
                            }
                        })(peers[i].ID));
                    }
                    $this.peers = peers;
                });
            } else {
                this.peers = [
                    {
                        ID: 0,
                        history: -8.64e+7,
                        Name: "Google DNS#1",
                        Address: "8.8.8.8",
                        AverageResponseTime: 38.5,
                        Uptime: 99.9,
                        Interval: 1000,
                    }
                ]
            }
        }
    });
});